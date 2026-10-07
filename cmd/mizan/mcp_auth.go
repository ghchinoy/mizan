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

// mcpDevSigningKey is the fallback HS256 signing key used only when JWT_SIGNING_KEY
// is unset — for local development. It mirrors the template's development-key
// fallback. Production deployments MUST set JWT_SIGNING_KEY.
const mcpDevSigningKey = "mizan-mcp-development-signing-key-change-me!"

// bearerAuth gates HTTP ingress behind a valid HS256-signed Bearer JWT.
type bearerAuth struct {
	signingKey []byte
}

// newBearerAuth builds the bearer gate, taking the HMAC signing key from
// JWT_SIGNING_KEY and falling back to a development key when it is unset.
func newBearerAuth() *bearerAuth {
	key := []byte(os.Getenv("JWT_SIGNING_KEY"))
	if len(key) == 0 {
		key = []byte(mcpDevSigningKey)
	}
	return &bearerAuth{signingKey: key}
}

// usingDevKey reports whether the fallback development signing key is in effect
// (i.e. JWT_SIGNING_KEY was not set). The http command warns when this is true.
func (a *bearerAuth) usingDevKey() bool {
	return string(a.signingKey) == mcpDevSigningKey
}

// requireBearer wraps next behind HS256 Bearer-JWT validation. A missing or
// invalid token yields 401 with an RFC 6750 WWW-Authenticate challenge; a valid
// token passes through. The token is read from the Authorization header
// ("Bearer <jwt>"), falling back to a ?token= query parameter (helpful for some
// browser SSE clients), matching the template's RequireBearer.
func (a *bearerAuth) requireBearer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tokenStr := ""
		if authHeader := r.Header.Get("Authorization"); strings.HasPrefix(authHeader, "Bearer ") {
			tokenStr = strings.TrimPrefix(authHeader, "Bearer ")
		}
		if tokenStr == "" {
			tokenStr = r.URL.Query().Get("token")
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
		})
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
// It mirrors the template's GenerateAccessToken and exists so operators can mint a
// local token against the same signing key the server validates (and so the auth
// middleware test can produce a valid token without a token server).
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
