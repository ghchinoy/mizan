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

// mcp_cmd_test.go asserts the command wiring: `mizan mcp` is registered on the
// root and exposes exactly the two transport sub-subcommands (stdio, http) with
// their distinguishing flags. This needs no cloud.

import "testing"

func TestMcpCommandWiredIntoRoot(t *testing.T) {
	root := newRootCmd()
	mcpCmd, _, err := root.Find([]string{"mcp"})
	if err != nil || mcpCmd.Name() != "mcp" {
		t.Fatalf("`mizan mcp` not wired into root: cmd=%v err=%v", mcpCmd, err)
	}

	sub := map[string]bool{}
	for _, c := range mcpCmd.Commands() {
		sub[c.Name()] = true
	}
	for _, want := range []string{"stdio", "http"} {
		if !sub[want] {
			t.Errorf("`mizan mcp` is missing the %q sub-subcommand (got %v)", want, sub)
		}
	}
}

func TestMcpHTTPHasPortFlag(t *testing.T) {
	cmd := newMcpHTTPCmd()
	f := cmd.Flags().Lookup("port")
	if f == nil {
		t.Fatal("`mizan mcp http` is missing the --port flag")
	}
	if f.DefValue != "8080" {
		t.Errorf("--port default = %q, want 8080", f.DefValue)
	}
}

func TestMcpStdioHasNoPortFlag(t *testing.T) {
	// stdio and http have disjoint flag sets; stdio must not carry --port.
	if f := newMcpStdioCmd().Flags().Lookup("port"); f != nil {
		t.Error("`mizan mcp stdio` should not have a --port flag")
	}
}
