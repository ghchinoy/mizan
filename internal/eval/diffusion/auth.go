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

package diffusion

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/oauth2/google"
	"google.golang.org/api/idtoken"
)

// TokenSource supplies a bearer token for each diffusion request.
type TokenSource interface {
	Token(ctx context.Context) (string, error)
}

// StaticToken is a fixed bearer token (e.g. from MIZAN_DIFFUSION_TOKEN).
type StaticToken string

// Token returns the static token.
func (s StaticToken) Token(context.Context) (string, error) { return string(s), nil }

// Auth modes accepted by ResolveAuth (config key diffusion-auth).
const (
	AuthAuto        = "auto"         // pick by endpoint: Vertex -> access token, remote https -> ID token, local -> none
	AuthNone        = "none"         // no Authorization header
	AuthAccessToken = "access-token" // OAuth2 access token (cloud-platform); Vertex AI /invoke endpoints
	AuthIDToken     = "id-token"     // OIDC identity token; Cloud Run IAM and IAP-fronted dgem gateways
)

// ResolveAuth picks a TokenSource for baseURL. A non-empty static token always
// wins. It returns (nil, nil) when no Authorization header should be sent.
func ResolveAuth(mode, baseURL, static string) (TokenSource, error) {
	if s := strings.TrimSpace(static); s != "" {
		return StaticToken(s), nil
	}
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		mode = AuthAuto
	}
	if mode == AuthAuto {
		switch {
		case IsVertexEndpointURL(baseURL):
			mode = AuthAccessToken
		case isRemoteHTTPS(baseURL):
			mode = AuthIDToken
		default:
			mode = AuthNone
		}
	}
	switch mode {
	case AuthNone:
		return nil, nil
	case AuthAccessToken:
		return &adcTokenSource{kind: AuthAccessToken}, nil
	case AuthIDToken:
		return &adcTokenSource{kind: AuthIDToken, audience: origin(baseURL)}, nil
	default:
		return nil, fmt.Errorf("diffusion: unknown auth mode %q (want auto|none|access-token|id-token)", mode)
	}
}

func isRemoteHTTPS(u string) bool {
	p, err := url.Parse(u)
	if err != nil || p.Scheme != "https" {
		return false
	}
	h := p.Hostname()
	return h != "localhost" && h != "127.0.0.1" && h != "::1"
}

func origin(u string) string {
	p, err := url.Parse(u)
	if err != nil || p.Scheme == "" || p.Host == "" {
		return u
	}
	return p.Scheme + "://" + p.Host
}

// adcTokenSource mints tokens from Application Default Credentials and caches
// them for 45 minutes. For authorized_user credentials (gcloud auth
// application-default login) it exchanges the refresh token directly, which
// yields both an access token and an ID token without shelling out to gcloud.
// Other credential types use the oauth2/google (access) and idtoken (identity)
// libraries, which cover service-account keys and the metadata server.
type adcTokenSource struct {
	kind     string
	audience string

	mu      sync.Mutex
	token   string
	expires time.Time
}

func (a *adcTokenSource) Token(ctx context.Context) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.token != "" && time.Now().Before(a.expires) {
		return a.token, nil
	}
	tok, err := a.mint(ctx)
	if err != nil {
		return "", err
	}
	a.token, a.expires = tok, time.Now().Add(45*time.Minute)
	return tok, nil
}

func (a *adcTokenSource) mint(ctx context.Context) (string, error) {
	if acc, id, ok := refreshAuthorizedUser(ctx); ok {
		if a.kind == AuthIDToken && id != "" {
			return id, nil
		}
		if a.kind == AuthAccessToken && acc != "" {
			return acc, nil
		}
	}
	if a.kind == AuthIDToken {
		ts, err := idtoken.NewTokenSource(ctx, a.audience)
		if err != nil {
			return "", fmt.Errorf("id token for %s: %w", a.audience, err)
		}
		t, err := ts.Token()
		if err != nil {
			return "", fmt.Errorf("id token for %s: %w", a.audience, err)
		}
		return t.AccessToken, nil
	}
	creds, err := google.FindDefaultCredentials(ctx, "https://www.googleapis.com/auth/cloud-platform")
	if err != nil {
		return "", fmt.Errorf("access token: %w", err)
	}
	t, err := creds.TokenSource.Token()
	if err != nil {
		return "", fmt.Errorf("access token: %w", err)
	}
	return t.AccessToken, nil
}

// refreshAuthorizedUser exchanges an authorized_user ADC refresh token for an
// access token and an ID token. ok is false when ADC is not authorized_user.
func refreshAuthorizedUser(ctx context.Context) (access, id string, ok bool) {
	path := strings.TrimSpace(os.Getenv("GOOGLE_APPLICATION_CREDENTIALS"))
	if path == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", "", false
		}
		path = filepath.Join(home, ".config", "gcloud", "application_default_credentials.json")
	}
	raw, err := os.ReadFile(path) //nolint:gosec // G703: the ADC path is operator-controlled (env or home dir), as in every Google SDK
	if err != nil {
		return "", "", false
	}
	var cred struct {
		Type         string `json:"type"`
		ClientID     string `json:"client_id"`
		ClientSecret string `json:"client_secret"`
		RefreshToken string `json:"refresh_token"`
	}
	if json.Unmarshal(raw, &cred) != nil || cred.Type != "authorized_user" || cred.RefreshToken == "" {
		return "", "", false
	}
	form := url.Values{
		"client_id":     {cred.ClientID},
		"client_secret": {cred.ClientSecret},
		"refresh_token": {cred.RefreshToken},
		"grant_type":    {"refresh_token"},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://oauth2.googleapis.com/token", strings.NewReader(form.Encode()))
	if err != nil {
		return "", "", false
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		return "", "", false
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", "", false
	}
	var tok struct {
		AccessToken string `json:"access_token"`
		IDToken     string `json:"id_token"`
	}
	if json.Unmarshal(body, &tok) != nil {
		return "", "", false
	}
	return strings.TrimSpace(tok.AccessToken), strings.TrimSpace(tok.IDToken), true
}
