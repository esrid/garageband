// Package oauth is the OAuth login port and its provider adapters (Google,
// Google today, one file per extra provider). Features depend on the Provider
// interface only, so swapping or adding a provider never touches feature code.
package oauth

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"

	"golang.org/x/oauth2"
)

// UserInfo is the provider-agnostic identity a provider returns.
type UserInfo struct {
	ProviderID string // stable unique id at the provider — never the email
	Email      string
	Name       string
}

// Provider is the port: authorization-code flow with PKCE, collapsed to the
// two calls a login feature needs.
type Provider interface {
	Name() string
	// AuthCodeURL returns the consent URL for this state and PKCE verifier.
	AuthCodeURL(state, verifier string) string
	// Authenticate trades the callback code (plus PKCE verifier) for the
	// user's identity.
	Authenticate(ctx context.Context, code, verifier string) (UserInfo, error)
}

// GenerateVerifier returns a PKCE code verifier (RFC 7636).
func GenerateVerifier() string { return oauth2.GenerateVerifier() }

func getJSON(ctx context.Context, client *http.Client, url string, header http.Header, into any) (err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	maps.Copy(req.Header, header)
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := resp.Body.Close(); cerr != nil && err == nil {
			err = cerr
		}
	}()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: unexpected status %d", url, resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(into)
}
