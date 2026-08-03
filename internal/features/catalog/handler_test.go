package catalog_test

import (
	"bytes"
	"context"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/esrid/garageband/internal/features/catalog"
)

func TestCatalogHTTPValidatesAndCreatesAnItem(t *testing.T) {
	fixture := newCatalogFixture(t)
	mux := catalogMux(fixture, fixture.ownerID)

	invalid := url.Values{
		"kind": {"service"}, "name": {"Vidange"}, "price_kind": {"fixed"},
		"amount": {"gratuit demain"}, "tax_basis": {"incl"}, "vat_rate": {"20"},
		"location_scope": {"selected"}, "location_ids": {fixture.locationA},
	}
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, formRequest(http.MethodPost, "/catalog", invalid))
	if response.Code != http.StatusUnprocessableEntity || !strings.Contains(response.Body.String(), "montant positif") {
		t.Fatalf("invalid create = %d %q", response.Code, response.Body.String())
	}
	overview, err := fixture.store.List(t.Context(), fixture.tenantID, fixture.ownerID, catalog.CatalogFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(overview.Items) != 0 {
		t.Fatalf("invalid form wrote %#v", overview.Items)
	}

	valid := url.Values{
		"kind": {"service"}, "name": {"Vidange complète"}, "reference": {"VID-WEB"},
		"description": {"Huile et filtre"}, "price_kind": {"from"}, "amount": {"89,90"},
		"tax_basis": {"incl"}, "vat_rate": {"20"}, "duration_minutes": {"60"},
		"location_scope": {"selected"}, "location_ids": {fixture.locationA},
	}
	response = httptest.NewRecorder()
	mux.ServeHTTP(response, formRequest(http.MethodPost, "/catalog", valid))
	if response.Code != http.StatusSeeOther || response.Header().Get("Location") != "/catalog?notice=saved" {
		t.Fatalf("valid create = %d location %q body %q", response.Code, response.Header().Get("Location"), response.Body.String())
	}
	response = httptest.NewRecorder()
	mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/catalog", nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "Vidange complète") || !strings.Contains(response.Body.String(), "89,90") {
		t.Fatalf("catalog list = %d %q", response.Code, response.Body.String())
	}
}

func TestCatalogHTTPRejectsMemberWrites(t *testing.T) {
	fixture := newCatalogFixture(t)
	mux := catalogMux(fixture, fixture.memberID)
	values := url.Values{
		"kind": {"service"}, "name": {"Intrusion"}, "price_kind": {"quote"},
		"tax_basis": {"incl"}, "vat_rate": {"20"}, "location_scope": {"all"},
	}
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, formRequest(http.MethodPost, "/catalog", values))
	if response.Code != http.StatusForbidden {
		t.Fatalf("member create = %d %q", response.Code, response.Body.String())
	}
}

func TestCatalogHTTPStagesAndConfirmsImport(t *testing.T) {
	fixture := newCatalogFixture(t)
	mux := catalogMux(fixture, fixture.ownerID)
	csv := "reference;name;type;price\nWEB-1;Diagnostic électronique;service;45\n"
	request := multipartRequest(t, "/catalog/imports", fixture.locationA, "catalog.csv", []byte(csv))
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusSeeOther || !strings.HasPrefix(response.Header().Get("Location"), "/catalog/imports/") {
		t.Fatalf("upload = %d location %q body %q", response.Code, response.Header().Get("Location"), response.Body.String())
	}
	previewPath := response.Header().Get("Location")
	response = httptest.NewRecorder()
	mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, previewPath+"?mode=merge", nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "Diagnostic électronique") || !strings.Contains(response.Body.String(), "1 ligne ajoutée") {
		t.Fatalf("preview = %d %q", response.Code, response.Body.String())
	}
	importID := strings.TrimPrefix(previewPath, "/catalog/imports/")
	response = httptest.NewRecorder()
	mux.ServeHTTP(response, formRequest(http.MethodPost, previewPath+"/publish", url.Values{"mode": {"merge"}}))
	if response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("unconfirmed publish = %d", response.Code)
	}
	response = httptest.NewRecorder()
	mux.ServeHTTP(response, formRequest(http.MethodPost, previewPath+"/publish", url.Values{"mode": {"merge"}, "confirm": {"1"}}))
	if response.Code != http.StatusSeeOther || response.Header().Get("Location") != "/catalog/imports/"+importID+"?notice=published" {
		t.Fatalf("publish = %d location %q body %q", response.Code, response.Header().Get("Location"), response.Body.String())
	}
	items, err := fixture.store.Quotable(t.Context(), fixture.tenantID, fixture.ownerID, fixture.locationA, "Diagnostic", mustDate(t, "2026-08-01"))
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].AmountCents == nil || *items[0].AmountCents != 4500 {
		t.Fatalf("published quote source = %#v", items)
	}
}

func catalogMux(fixture catalogFixture, userID string) *http.ServeMux {
	mux := http.NewServeMux()
	catalog.Register(
		mux, fixture.store,
		func(next http.Handler) http.Handler { return next },
		func(context.Context) (catalog.Principal, bool) {
			return catalog.Principal{UserID: userID, TenantID: fixture.tenantID}, true
		},
	)
	return mux
}

func formRequest(method, target string, values url.Values) *http.Request {
	request := httptest.NewRequest(method, target, strings.NewReader(values.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return request
}

func multipartRequest(t *testing.T, target, locationID, filename string, content []byte) *http.Request {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("location_id", locationID); err != nil {
		t.Fatal(err)
	}
	part, err := writer.CreateFormFile("file", filename)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(part, bytes.NewReader(content)); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, target, &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	return request
}
