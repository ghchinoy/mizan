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

// mcp_stdio.go is a thin transport file: it imports the go-sdk `mcp` package
// (allowed by the containment rule, design §2.1) only to run the shared server
// over the SDK's stdio transport. stdio is a local process pipe, so it carries NO
// auth (design §6).

import (
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/cobra"

	"github.com/ghchinoy/mizan/internal/mcpserver"
)

// newMcpStdioCmd wires `mizan mcp stdio`: serve the four MCP tools over the SDK's
// stdio transport. Intended for local/subprocess clients that spawn mizan and
// speak MCP over the child's stdin/stdout. No auth, no network flags.
func newMcpStdioCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stdio",
		Short: "Serve MCP over stdio (no auth; for local subprocess clients)",
		Long: "Serve the mizan MCP tools over the stdio transport.\n\n" +
			"The server reads JSON-RPC from stdin and writes to stdout, the\n" +
			"conventional transport for a client that spawns mizan as a child\n" +
			"process. stdio is a local pipe and carries no authentication. The\n" +
			"command blocks until the client disconnects or the process is signalled.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps, cleanup, err := buildMcpServer(cmd.Context())
			if err != nil {
				return err
			}
			defer func() { _ = cleanup() }()

			srv := mcpserver.NewServer(*deps)
			// Run blocks until the client disconnects or the context is cancelled.
			return srv.Run(cmd.Context(), &mcp.StdioTransport{})
		},
	}
}
