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

// mcp_auth.go ports the mcp-server-go-template's stateless bearer-JWT ingress gate
// (template/auth.go RequireBearer + GenerateAccessToken) down to the subset the
// `mizan mcp http` transport needs: HS256 Bearer validation as HTTP middleware,
// with the signing key read from JWT_SIGNING_KEY. The full OAuth 2.1 / DCR / PKCE
// authorization-server surface from the template is intentionally NOT ported —
// #114 asks for a bearer/JWT gate with a DISABLE_AUTH toggle, not a token-issuing
// authorization server. stdio carries no auth (local pipe).

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// minSigningKeyBytes is the minimum accepted length for the HS256 signing key.
// A short key weakens the HMAC; a real deployment key must be at least this long.
const minSigningKeyBytes = 32

// bearerAuth gates HTTP ingress behind a valid HS256-signed Bearer JWT.
type bearerAuth struct {
	signingKey []byte
}

// newBearerAuth builds the bearer gate from the HMAC signing key in
// JWT_SIGNING_KEY. It FAILS CLOSED: there is no shipped/default key fallback, so
// when JWT_SIGNING_KEY is unset or shorter than minSigningKeyBytes it returns an
// error and the HTTP transport must not serve. The only no-auth path is the
// explicit DISABLE_AUTH=true toggle (loopback-bound local development), handled by
// the caller before newBearerAuth is reached.
func newBearerAuth() (*bearerAuth, error) {
	key := []byte(os.Getenv("JWT_SIGNING_KEY"))
	if len(key) < minSigningKeyBytes {
		return nil, fmt.Errorf("JWT_SIGNING_KEY must be set (>=%d bytes) to serve MCP over HTTP; "+
			"set DISABLE_AUTH=true only for loopback-bound local development", minSigningKeyBytes)
	}
	return &bearerAuth{signingKey: key}, nil
}

// requireBearer wraps next behind HS256 Bearer-JWT validation. A missing or
// invalid token yields 401 with an RFC 6750 WWW-Authenticate challenge; a valid
// token passes through. The token is read from the Authorization header
// ("Bearer <jwt>") ONLY — a token in a URL/query parameter would leak into access
// logs, proxies, and Referer headers, and the streamable-HTTP transport does not
// need the query path. A valid token is also REQUIRED to carry an exp claim
// (jwt.WithExpirationRequired), so a forever-valid token is rejected.
func (a *bearerAuth) requireBearer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tokenStr := ""
		if authHeader := r.Header.Get("Authorization"); strings.HasPrefix(authHeader, "Bearer ") {
			tokenStr = strings.TrimPrefix(authHeader, "Bearer ")
		}
		if tokenStr == "" {
			a.respondUnauthorized(w, "missing_token", "access token is required")
			return
		}

		token, err := jwt.Parse(tokenStr, func(t *jwt.Token) (interface{}, error) {
			if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
			}
			return a.signingKey, nil
		}, jwt.WithExpirationRequired())
		if err != nil || !token.Valid {
			a.respondUnauthorized(w, "invalid_token", "access token is invalid or expired")
			return
		}

		next.ServeHTTP(w, r)
	})
}

// respondUnauthorized writes a 401 with an RFC 6750 Bearer challenge and a small
// JSON error body.
func (a *bearerAuth) respondUnauthorized(w http.ResponseWriter, errCode, errDesc string) {
	w.Header().Set("WWW-Authenticate", fmt.Sprintf(`Bearer error=%q, error_description=%q`, errCode, errDesc))
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"error":             errCode,
		"error_description": errDesc,
	})
}

// generateToken issues a signed HS256 Bearer token for the given subject and TTL.
// It mirrors the template's GenerateAccessToken. It is unexported and has no CLI
// surface today — its only caller is the auth middleware test, which needs to mint
// a valid token (always carrying an exp claim) against the same signing key the
// server validates, without standing up a token server. It is NOT an operator mint
// path; expose a subcommand first if operator minting is ever required.
func (a *bearerAuth) generateToken(subject string, ttl time.Duration) (string, error) {
	now := time.Now()
	claims := jwt.MapClaims{
		"sub": subject,
		"iat": now.Unix(),
		"exp": now.Add(ttl).Unix(),
		"typ": "access",
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(a.signingKey)
}
