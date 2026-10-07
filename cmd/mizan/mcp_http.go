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

// defaultMcpHTTPAddress binds loopback by default so the first `mizan mcp http`
// does not expose the server to the local network/host. Operators who deliberately
// expose it pass --address 0.0.0.0 (which requires a real JWT_SIGNING_KEY).
const defaultMcpHTTPAddress = "127.0.0.1"

// newMcpHTTPCmd wires `mizan mcp http`: serve the four MCP tools over the SDK's
// streamable-HTTP transport (Stateless), mounted on an http.Server. Auth is ON by
// default and FAILS CLOSED — a stateless HS256 Bearer-JWT gate (mcp_auth.go)
// requires a real JWT_SIGNING_KEY (>=32 bytes); there is no shipped default key.
// The only no-auth path is the explicit DISABLE_AUTH=true toggle (local dev).
func newMcpHTTPCmd() *cobra.Command {
	var port int
	var address string
	var allowLocalFiles bool
	cmd := &cobra.Command{
		Use:   "http",
		Short: "Serve MCP over streamable HTTP (bearer-JWT auth; DISABLE_AUTH=true to bypass)",
		Long: "Serve the mizan MCP tools over the streamable-HTTP transport.\n\n" +
			"The server mounts the MCP endpoint at / and a liveness probe at\n" +
			"/healthz. Requests are gated by a stateless HS256 Bearer-JWT check\n" +
			"whose signing key is read from JWT_SIGNING_KEY (required, >=32 bytes);\n" +
			"the server FAILS TO START with auth enabled and no valid key. Set\n" +
			"DISABLE_AUTH=true to bypass the gate for local development (never expose\n" +
			"an unauthenticated server to public ingress).\n\n" +
			"The server binds 127.0.0.1 by default; --address 0.0.0.0 exposes it on\n" +
			"all interfaces and requires a real JWT_SIGNING_KEY. By default local\n" +
			"file:// inputs are DISABLED on this transport (callers may be remote and\n" +
			"untrusted); use gcs: or text:. --allow-local-files re-enables them and is\n" +
			"UNSAFE for untrusted callers (it lets a caller read any file the server\n" +
			"process can access).\n\n" +
			"SECURITY — least-privilege service account: a caller may set the\n" +
			"project/location per call and pass gs:// URIs, which run under the\n" +
			"SERVER's ADC/service-account identity (confused-deputy, by design per the\n" +
			"owner's locked decision). When exposing this server, its service account\n" +
			"MUST be least-privilege, scoped to exactly the intended project(s) and\n" +
			"bucket(s). Optionally set MIZAN_MCP_ALLOWED_PROJECTS (CSV) to reject\n" +
			"per-call project overrides outside that list.\n\n" +
			"The command blocks until the process is signalled (SIGINT/SIGTERM), then\n" +
			"shuts down gracefully.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			// Fail closed BEFORE opening any service: with auth enabled and no valid
			// JWT_SIGNING_KEY, build the gate first so the command errors out instead
			// of serving.
			authDisabled := os.Getenv("DISABLE_AUTH") == "true"
			var auth *bearerAuth
			if authDisabled {
				fmt.Fprintln(cmd.ErrOrStderr(),
					"mizan: warning: MCP HTTP authentication is DISABLED (DISABLE_AUTH=true); do not expose to public ingress")
			} else {
				a, err := newBearerAuth()
				if err != nil {
					return err
				}
				auth = a
			}

			deps, cleanup, err := buildMcpServer(cmd.Context(), allowLocalFiles)
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

			// Origin verification is OFF by default in go-sdk v1.7.0; wrap the handler
			// with cross-origin protection (defense-in-depth against DNS-rebinding).
			var mcpHandler http.Handler = http.NewCrossOriginProtection().Handler(streamable)
			if auth != nil {
				mcpHandler = auth.requireBearer(mcpHandler)
			}

			mux := http.NewServeMux()
			mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusOK)
				_, _ = fmt.Fprintln(w, "ok")
			})
			mux.Handle("/", mcpHandler)

			server := &http.Server{
				Addr:              fmt.Sprintf("%s:%d", address, port),
				Handler:           mux,
				ReadHeaderTimeout: 10 * time.Second,
			}
			return serveMcpHTTP(cmd.Context(), cmd, server)
		},
	}
	cmd.Flags().IntVar(&port, "port", defaultMcpHTTPPort, "port to listen on")
	cmd.Flags().StringVar(&address, "address", defaultMcpHTTPAddress,
		"bind address (use 0.0.0.0 to expose on all interfaces; requires a real JWT_SIGNING_KEY)")
	cmd.Flags().BoolVar(&allowLocalFiles, "allow-local-files", false,
		"allow local file: inputs over HTTP (UNSAFE for untrusted callers; lets a caller read server-side files)")
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
