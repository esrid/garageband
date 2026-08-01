package oauth

import (
	"context"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/endpoints"
)

// Google implements Provider over Google OpenID Connect.
// Verified against
// https://developers.google.com/identity/openid-connect/openid-connect
// 2026-08-01: scopes openid/email/profile, userinfo endpoint below, `sub` is
// the stable id.
type Google struct{ cfg oauth2.Config }

func NewGoogle(clientID, clientSecret, redirectURL string) *Google {
	return &Google{cfg: oauth2.Config{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		Endpoint:     endpoints.Google,
		RedirectURL:  redirectURL,
		Scopes:       []string{"openid", "email", "profile"},
	}}
}

func (g *Google) Name() string { return "google" }

func (g *Google) AuthCodeURL(state, verifier string) string {
	return g.cfg.AuthCodeURL(state, oauth2.S256ChallengeOption(verifier))
}

func (g *Google) Authenticate(ctx context.Context, code, verifier string) (UserInfo, error) {
	tok, err := g.cfg.Exchange(ctx, code, oauth2.VerifierOption(verifier))
	if err != nil {
		return UserInfo{}, err
	}
	var claims struct {
		Sub   string `json:"sub"`
		Email string `json:"email"`
		Name  string `json:"name"`
	}
	err = getJSON(ctx, g.cfg.Client(ctx, tok),
		"https://openidconnect.googleapis.com/v1/userinfo", nil, &claims)
	if err != nil {
		return UserInfo{}, err
	}
	return UserInfo{ProviderID: claims.Sub, Email: claims.Email, Name: claims.Name}, nil
}
