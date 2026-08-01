package businesslookup_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/esrid/garageband/internal/platform/businesslookup"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

func TestRechercheEntreprisesFindsRequestedEstablishment(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if got := request.URL.Query().Get("q"); got != "12345678901234" {
			t.Errorf("q = %q", got)
		}
		if got := request.URL.Query().Get("per_page"); got != "1" {
			t.Errorf("per_page = %q", got)
		}
		if got := request.Header.Get("User-Agent"); got != "garageband/test" {
			t.Errorf("User-Agent = %q", got)
		}
		body := `{
          "results": [{
            "siren": "123456789",
            "nom_complet": "GARAGE EXEMPLE",
            "nom_raison_sociale": "GARAGE EXEMPLE SAS",
            "siege": {"siret": "12345678900010"},
            "matching_etablissements": [{
              "siret": "12345678901234",
              "nom_commercial": "Garage Exemple Fort-de-France",
              "adresse": "12 RUE DES MECANICIENS",
              "code_postal": "97200",
              "libelle_commune": "FORT-DE-FRANCE"
            }]
          }]
        }`
		return response(http.StatusOK, body), nil
	})}

	provider := businesslookup.NewRechercheEntreprises(client, "https://registry.example", "garageband/test")
	profile, err := provider.Enrich(context.Background(), businesslookup.Request{SIRET: "12345678901234"})
	if err != nil {
		t.Fatal(err)
	}
	if profile.SIRET != "12345678901234" || profile.SIREN != "123456789" {
		t.Fatalf("unexpected identifiers: %#v", profile)
	}
	if profile.LegalName != "GARAGE EXEMPLE SAS" || profile.TradingName != "Garage Exemple Fort-de-France" {
		t.Fatalf("unexpected names: %#v", profile)
	}
	if profile.Address.PostalCode != "97200" || profile.Address.City != "FORT-DE-FRANCE" {
		t.Fatalf("unexpected address: %#v", profile.Address)
	}
}

func TestRechercheEntreprisesRejectsUnsupportedAndMissingSources(t *testing.T) {
	provider := businesslookup.NewRechercheEntreprises(nil, "", "")
	if _, err := provider.Enrich(context.Background(), businesslookup.Request{WebsiteURL: "https://example.test"}); !errors.Is(err, businesslookup.ErrUnsupported) {
		t.Fatalf("website error = %v", err)
	}
	if _, err := provider.Enrich(context.Background(), businesslookup.Request{SIRET: "not-a-siret"}); err == nil {
		t.Fatal("invalid SIRET accepted")
	}
}

func TestRechercheEntreprisesReturnsNotFound(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		return response(http.StatusOK, `{"results":[]}`), nil
	})}
	provider := businesslookup.NewRechercheEntreprises(client, "https://registry.example", "")
	_, err := provider.Enrich(context.Background(), businesslookup.Request{SIRET: "12345678901234"})
	if !errors.Is(err, businesslookup.ErrNotFound) {
		t.Fatalf("error = %v, want ErrNotFound", err)
	}
}

func TestRechercheEntreprisesRejectsClosedEstablishment(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		return response(http.StatusOK, `{
			"results":[{
				"siren":"123456789",
				"matching_etablissements":[{
					"siret":"12345678901234",
					"etat_administratif":"F"
				}]
			}]
		}`), nil
	})}
	provider := businesslookup.NewRechercheEntreprises(client, "https://registry.example", "")
	_, err := provider.Enrich(context.Background(), businesslookup.Request{SIRET: "12345678901234"})
	if !errors.Is(err, businesslookup.ErrInactive) {
		t.Fatalf("error = %v, want ErrInactive", err)
	}
}

func response(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Status:     http.StatusText(status),
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}
