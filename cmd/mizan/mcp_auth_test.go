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

// mcp_auth_test.go unit-tests the bearer-JWT ingress gate against an
// httptest.Server (no network to a real token server): a locally signed HS256
// token passes, a missing/invalid/wrong-key token is rejected with 401, and the
// DISABLE_AUTH bypass (no middleware) lets requests through.

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// okHandler is the protected downstream handler: it 200s so the test can
// distinguish "passed the gate" from "rejected by the gate".
var okHandler = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
})

func TestRequireBearerValidToken(t *testing.T) {
	auth := &bearerAuth{signingKey: []byte("unit-test-signing-key")}
	srv := httptest.NewServer(auth.requireBearer(okHandler))
	defer srv.Close()

	token, err := auth.generateToken("tester", time.Hour)
	if err != nil {
		t.Fatalf("generate token: %v", err)
	}
	req, _ := http.NewRequest(http.MethodPost, srv.URL, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200 for a valid bearer token", resp.StatusCode)
	}
}

func TestRequireBearerMissingToken(t *testing.T) {
	auth := &bearerAuth{signingKey: []byte("unit-test-signing-key")}
	srv := httptest.NewServer(auth.requireBearer(okHandler))
	defer srv.Close()

	resp, err := srv.Client().Post(srv.URL, "application/json", nil)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401 when the token is missing", resp.StatusCode)
	}
	if ch := resp.Header.Get("WWW-Authenticate"); ch == "" {
		t.Error("expected a WWW-Authenticate challenge header on 401")
	}
}

func TestRequireBearerInvalidToken(t *testing.T) {
	auth := &bearerAuth{signingKey: []byte("unit-test-signing-key")}
	srv := httptest.NewServer(auth.requireBearer(okHandler))
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodPost, srv.URL, nil)
	req.Header.Set("Authorization", "Bearer not-a-real-jwt")
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401 for a malformed token", resp.StatusCode)
	}
}

func TestRequireBearerWrongKeyRejected(t *testing.T) {
	signer := &bearerAuth{signingKey: []byte("signer-key")}
	verifier := &bearerAuth{signingKey: []byte("different-verifier-key")}
	srv := httptest.NewServer(verifier.requireBearer(okHandler))
	defer srv.Close()

	// A token signed with a DIFFERENT key must fail signature verification.
	token, err := signer.generateToken("tester", time.Hour)
	if err != nil {
		t.Fatalf("generate token: %v", err)
	}
	req, _ := http.NewRequest(http.MethodPost, srv.URL, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401 for a token signed with the wrong key", resp.StatusCode)
	}
}

func TestRequireBearerExpiredTokenRejected(t *testing.T) {
	auth := &bearerAuth{signingKey: []byte("unit-test-signing-key")}
	srv := httptest.NewServer(auth.requireBearer(okHandler))
	defer srv.Close()

	token, err := auth.generateToken("tester", -time.Minute) // already expired
	if err != nil {
		t.Fatalf("generate token: %v", err)
	}
	req, _ := http.NewRequest(http.MethodPost, srv.URL, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401 for an expired token", resp.StatusCode)
	}
}

// TestDisableAuthBypass models the DISABLE_AUTH=true path: the http command mounts
// the handler WITHOUT the bearer middleware, so an unauthenticated request passes.
func TestDisableAuthBypass(t *testing.T) {
	srv := httptest.NewServer(okHandler) // no requireBearer wrapper
	defer srv.Close()

	resp, err := srv.Client().Post(srv.URL, "application/json", nil)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200 when auth is disabled", resp.StatusCode)
	}
}

// a validEnvKey is a 40-byte signing key (>= minSigningKeyBytes).
const validEnvKey = "env-provided-signing-key-that-is-long-ok"

func TestNewBearerAuthUsesEnvKey(t *testing.T) {
	t.Setenv("JWT_SIGNING_KEY", validEnvKey)
	auth, err := newBearerAuth()
	if err != nil {
		t.Fatalf("newBearerAuth() with a valid key: %v", err)
	}
	if string(auth.signingKey) != validEnvKey {
		t.Errorf("signingKey = %q, want the env value", string(auth.signingKey))
	}
	// A key this long mints and validates a token end-to-end.
	token, err := auth.generateToken("tester", time.Hour)
	if err != nil {
		t.Fatalf("generate token: %v", err)
	}
	srv := httptest.NewServer(auth.requireBearer(okHandler))
	defer srv.Close()
	req, _ := http.NewRequest(http.MethodPost, srv.URL, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200 for a token minted with a real key", resp.StatusCode)
	}
}

// TestNewBearerAuthFailsClosedWithoutKey proves the fail-closed posture: with auth
// enabled and no (or too-short) JWT_SIGNING_KEY, newBearerAuth returns an error so
// the http command does NOT serve. The dev-key fallback is gone.
func TestNewBearerAuthFailsClosedWithoutKey(t *testing.T) {
	cases := []struct {
		name string
		key  string
	}{
		{"unset", ""},
		{"too short", "short-key"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("JWT_SIGNING_KEY", tc.key)
			auth, err := newBearerAuth()
			if err == nil {
				t.Fatalf("newBearerAuth() = %v, nil; want a fail-closed error for key %q", auth, tc.key)
			}
		})
	}
}
