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

// mcp_http.go is a thin transport file: it imports the go-sdk `mcp` package
// (allowed by the containment rule, design §2.1) only to mount the shared server
// as a streamable-HTTP handler. It reuses the template's bearer-JWT gate
// (mcp_auth.go) as middleware, honoring DISABLE_AUTH=true for local dev.

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/cobra"

	"github.com/ghchinoy/mizan/internal/mcpserver"
)

// defaultMcpHTTPPort mirrors the template's default listen port.
const defaultMcpHTTPPort = 8080

// newMcpHTTPCmd wires `mizan mcp http`: serve the four MCP tools over the SDK's
// streamable-HTTP transport (Stateless), mounted on an http.Server. Auth is ON by
// default — a stateless HS256 Bearer-JWT gate (mcp_auth.go, ported from the
// template) — and bypassed with DISABLE_AUTH=true for local development. The
// signing key comes from JWT_SIGNING_KEY.
func newMcpHTTPCmd() *cobra.Command {
	var port int
	cmd := &cobra.Command{
		Use:   "http",
		Short: "Serve MCP over streamable HTTP (bearer-JWT auth; DISABLE_AUTH=true to bypass)",
		Long: "Serve the mizan MCP tools over the streamable-HTTP transport.\n\n" +
			"The server mounts the MCP endpoint at / and a liveness probe at\n" +
			"/healthz. Requests are gated by a stateless HS256 Bearer-JWT check\n" +
			"whose signing key is read from JWT_SIGNING_KEY; set DISABLE_AUTH=true to\n" +
			"bypass the gate for local development (never expose an unauthenticated\n" +
			"server to public ingress). The command blocks until the process is\n" +
			"signalled (SIGINT/SIGTERM), then shuts down gracefully.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps, cleanup, err := buildMcpServer(cmd.Context())
			if err != nil {
				return err
			}
			defer func() { _ = cleanup() }()

			srv := mcpserver.NewServer(*deps)
			// One shared server backs every session (Stateless: fresh temp session
			// per request, no Mcp-Session-Id), matching the template.
			streamable := mcp.NewStreamableHTTPHandler(
				func(*http.Request) *mcp.Server { return srv },
				&mcp.StreamableHTTPOptions{Stateless: true},
			)

			var mcpHandler http.Handler = streamable
			if os.Getenv("DISABLE_AUTH") == "true" {
				fmt.Fprintln(cmd.ErrOrStderr(),
					"mizan: warning: MCP HTTP authentication is DISABLED (DISABLE_AUTH=true); do not expose to public ingress")
			} else {
				auth := newBearerAuth()
				if auth.usingDevKey() {
					fmt.Fprintln(cmd.ErrOrStderr(),
						"mizan: warning: JWT_SIGNING_KEY is unset; using the built-in development signing key (set JWT_SIGNING_KEY for production)")
				}
				mcpHandler = auth.requireBearer(streamable)
			}

			mux := http.NewServeMux()
			mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusOK)
				_, _ = fmt.Fprintln(w, "ok")
			})
			mux.Handle("/", mcpHandler)

			server := &http.Server{
				Addr:              fmt.Sprintf(":%d", port),
				Handler:           mux,
				ReadHeaderTimeout: 10 * time.Second,
			}
			return serveMcpHTTP(cmd.Context(), cmd, server)
		},
	}
	cmd.Flags().IntVar(&port, "port", defaultMcpHTTPPort, "port to listen on")
	return cmd
}

// serveMcpHTTP runs server.ListenAndServe and shuts it down gracefully when the
// parent context is cancelled or an interrupt/termination signal arrives. It
// returns nil on a clean shutdown and the listen error otherwise.
func serveMcpHTTP(ctx context.Context, cmd *cobra.Command, server *http.Server) error {
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		fmt.Fprintf(cmd.ErrOrStderr(), "mizan: serving MCP over streamable HTTP on %s\n", server.Addr)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
			return
		}
		errCh <- nil
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return server.Shutdown(shutdownCtx)
	}
}
