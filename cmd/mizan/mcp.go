// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

// mcp.go wires the `mizan mcp` command family: a parent group with two transport
// sub-subcommands (`stdio`, `http`) that both serve the SAME four tools over the
// same SDK-agnostic core (internal/mcpserver). The command layer is the thin
// transport wrapper described in design §3/§6: it builds the service deps ONCE
// from config via the wire composition root, hands them to mcpserver.NewServer,
// and mounts the resulting server on a transport. No eval logic lives here.
//
// Dependency direction mirrors the rest of cmd/mizan: this file depends only on
// the service façades (registry.Service / eval.Engine / results.Service) + config
// through internal/wire, plus internal/mcpserver. The go-sdk `mcp` package is
// imported only by the thin transport files (mcp_stdio.go, mcp_http.go) and
// internal/mcpserver/server.go (the containment rule, design §2.1).

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/ghchinoy/mizan/internal/eval"
	"github.com/ghchinoy/mizan/internal/mcpserver"
	"github.com/ghchinoy/mizan/internal/wire"
)

// newMcpCmd wires the `mizan mcp` command family (CUJ: expose the eval engine as
// an MCP server). Transport selection is via sub-subcommands — `mizan mcp stdio`
// and `mizan mcp http` — mirroring the `eval run` / `eval pairwise` idiom, so each
// transport keeps its own disjoint flag set (http needs --port + auth toggles;
// stdio needs none).
func newMcpCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "mcp",
		Short:   "Serve the mizan eval engine over the Model Context Protocol (MCP)",
		Long: "Serve the mizan eval engine over the Model Context Protocol (MCP).\n\n" +
			"The server exposes four tools — mizan_list_metrics, mizan_get_metric,\n" +
			"mizan_eval_run, and mizan_eval_pairwise — each mapping onto an existing\n" +
			"internal method the CLI already calls. Both transports read the SAME\n" +
			"configuration from the process environment (project/location/staging\n" +
			"bucket); project and location are additionally overridable per call.\n\n" +
			"Transports:\n" +
			"  mizan mcp stdio   serve over stdio (no auth; for local subprocess clients)\n" +
			"  mizan mcp http    serve over streamable HTTP (bearer-JWT auth)",
		GroupID: groupMCP,
	}
	cmd.AddCommand(newMcpStdioCmd())
	cmd.AddCommand(newMcpHTTPCmd())
	return cmd
}

// buildMcpServer builds the shared MCP server from config via the wire
// composition root (the ONLY place the cmd layer opens the services). It opens
// the registry, results, and default eval engine exactly as the eval commands do,
// assembles mcpserver.Deps (including the EngineFor factory for per-call
// project/location overrides), and returns the registered *mcp.Server plus a
// cleanup that closes every opened service in reverse order.
//
// It returns an untyped server via newServer so this file stays SDK-free; the
// concrete *mcp.Server construction (mcpserver.NewServer) is SDK-agnostic from the
// cmd layer's point of view — only the transport files touch the go-sdk types.
func buildMcpServer(ctx context.Context) (*mcpserver.Deps, func() error, error) {
	cfg, err := mustConfig()
	if err != nil {
		return nil, nil, err
	}
	// Both transports run live eval calls, so a project is required at startup
	// (mirrors openEngine). Per-call project/location overrides are applied on top
	// via EngineFor below; the bucket stays fixed at server start (design §4/§8 Q5).
	if err := requireProject(cfg); err != nil {
		return nil, nil, err
	}

	var closers []func() error
	cleanup := func() error {
		var firstErr error
		// Close in reverse order of opening.
		for i := len(closers) - 1; i >= 0; i-- {
			if cerr := closers[i](); cerr != nil && firstErr == nil {
				firstErr = cerr
			}
		}
		return firstErr
	}
	// On any error after we have opened a service, release what we already hold.
	ok := false
	defer func() {
		if !ok {
			_ = cleanup()
		}
	}()

	reg, closeReg, err := wire.OpenService(cfg)
	if err != nil {
		return nil, nil, fmt.Errorf("open registry service: %w", err)
	}
	closers = append(closers, closeReg)

	res, closeRes, err := wire.OpenResultService(cfg)
	if err != nil {
		return nil, nil, fmt.Errorf("open results service: %w", err)
	}
	closers = append(closers, closeRes)

	eng, closeEng, err := wire.NewEngine(ctx, cfg)
	if err != nil {
		return nil, nil, fmt.Errorf("open eval engine: %w", err)
	}
	closers = append(closers, closeEng)

	// EngineFor builds a REQUEST-SCOPED engine for a per-call project/location
	// override (design §8 Q5). It copies the server's config so the default engine
	// and its config are untouched, applies the project override via the same
	// applyProjectOverride precedence the eval commands use, sets the location
	// directly (applyProjectOverride governs only the project), and builds a fresh
	// engine. The returned close func is invoked by the mcpserver handler after the
	// run. The staging bucket is inherited from the server config unchanged.
	engineFor := func(ctx context.Context, project, location string) (*eval.Engine, func() error, error) {
		c := *cfg
		// Detach from the shared Sources map so applyProjectOverride's source
		// re-attribution never mutates the server default's config.
		c.Sources = nil
		applyProjectOverride(&c, project)
		if location != "" {
			c.Location = location
		}
		return wire.NewEngine(ctx, &c)
	}

	deps := &mcpserver.Deps{
		Registry:  reg,
		Engine:    eng,
		Results:   res,
		Config:    cfg,
		EngineFor: engineFor,
	}
	ok = true
	return deps, cleanup, nil
}
