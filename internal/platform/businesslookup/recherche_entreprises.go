package businesslookup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

const officialBaseURL = "https://recherche-entreprises.api.gouv.fr"

var (
	ErrNotFound    = errors.New("business not found")
	ErrInactive    = errors.New("business establishment is inactive")
	ErrUnsupported = errors.New("business lookup source is unsupported")
)

// RechercheEntreprises enriches a French establishment from the official
// Recherche d'entreprises API. Website crawling is intentionally left to a
// separate provider because it needs SSRF protections and different trust
// semantics.
type RechercheEntreprises struct {
	client    *http.Client
	baseURL   string
	userAgent string
}

func NewRechercheEntreprises(client *http.Client, baseURL, userAgent string) *RechercheEntreprises {
	if client == nil {
		client = http.DefaultClient
	}
	if strings.TrimSpace(baseURL) == "" {
		baseURL = officialBaseURL
	}
	return &RechercheEntreprises{
		client:    client,
		baseURL:   strings.TrimRight(baseURL, "/"),
		userAgent: userAgent,
	}
}

func (p *RechercheEntreprises) Name() string { return "recherche-entreprises" }

func (p *RechercheEntreprises) Enrich(ctx context.Context, request Request) (_ Profile, returnErr error) {
	siret := strings.TrimSpace(request.SIRET)
	if siret == "" && strings.TrimSpace(request.WebsiteURL) != "" {
		return Profile{}, ErrUnsupported
	}
	if !validSIRET(siret) {
		return Profile{}, fmt.Errorf("SIRET must contain exactly 14 digits")
	}

	endpoint, err := url.Parse(p.baseURL + "/search")
	if err != nil {
		return Profile{}, err
	}
	query := endpoint.Query()
	query.Set("q", siret)
	query.Set("per_page", "1")
	endpoint.RawQuery = query.Encode()

	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return Profile{}, err
	}
	if p.userAgent != "" {
		httpRequest.Header.Set("User-Agent", p.userAgent)
	}

	response, err := p.client.Do(httpRequest)
	if err != nil {
		return Profile{}, err
	}
	defer func() { returnErr = errors.Join(returnErr, response.Body.Close()) }()
	if response.StatusCode == http.StatusNotFound {
		return Profile{}, ErrNotFound
	}
	if response.StatusCode != http.StatusOK {
		if _, err := io.Copy(io.Discard, io.LimitReader(response.Body, 4<<10)); err != nil {
			return Profile{}, errors.Join(fmt.Errorf("business registry returned %s", response.Status), err)
		}
		return Profile{}, fmt.Errorf("business registry returned %s", response.Status)
	}

	var payload searchResponse
	decoder := json.NewDecoder(io.LimitReader(response.Body, 2<<20))
	if err := decoder.Decode(&payload); err != nil {
		return Profile{}, fmt.Errorf("decode business registry response: %w", err)
	}
	if len(payload.Results) == 0 {
		return Profile{}, ErrNotFound
	}

	result := payload.Results[0]
	establishment, ok := matchingEstablishment(result.MatchingEstablishments, siret)
	if !ok && result.HeadOffice.SIRET == siret {
		establishment = result.HeadOffice
		ok = true
	}
	if !ok {
		return Profile{}, ErrNotFound
	}
	if establishment.AdministrativeStatus == "F" {
		return Profile{}, ErrInactive
	}
	raw, err := json.Marshal(result)
	if err != nil {
		return Profile{}, err
	}
	legalName := firstNonEmpty(result.LegalName, result.FullName)
	return Profile{
		SIREN:       result.SIREN,
		SIRET:       establishment.SIRET,
		LegalName:   legalName,
		TradingName: firstNonEmpty(establishment.TradingName, legalName),
		Address: Address{
			Line1:       establishment.Address,
			PostalCode:  establishment.PostalCode,
			City:        establishment.City,
			CountryCode: "FR",
		},
		Raw: raw,
	}, nil
}

type searchResponse struct {
	Results []registryResult `json:"results"`
}

type registryResult struct {
	SIREN                  string                  `json:"siren"`
	FullName               string                  `json:"nom_complet"`
	LegalName              string                  `json:"nom_raison_sociale"`
	HeadOffice             registryEstablishment   `json:"siege"`
	MatchingEstablishments []registryEstablishment `json:"matching_etablissements"`
}

type registryEstablishment struct {
	SIRET                string `json:"siret"`
	TradingName          string `json:"nom_commercial"`
	Address              string `json:"adresse"`
	PostalCode           string `json:"code_postal"`
	City                 string `json:"libelle_commune"`
	AdministrativeStatus string `json:"etat_administratif"`
}

func matchingEstablishment(establishments []registryEstablishment, siret string) (registryEstablishment, bool) {
	for _, establishment := range establishments {
		if establishment.SIRET == siret {
			return establishment, true
		}
	}
	return registryEstablishment{}, false
}

func validSIRET(value string) bool {
	if len(value) != 14 {
		return false
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			return false
		}
	}
	return true
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
