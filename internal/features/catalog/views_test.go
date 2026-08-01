package catalog_test

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/a-h/templ"
	"github.com/esrid/garageband/internal/features/catalog"
)

func TestCatalogViewExplainsWhichPricesAreQuotable(t *testing.T) {
	now := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	past := time.Date(2026, 7, 31, 0, 0, 0, 0, time.UTC)
	page := catalog.Index{
		Organization: "Garage Test", Now: now, CanManage: true,
		Counts:    map[string]int{catalog.KindService: 2},
		Locations: []catalog.LocationRef{{ID: "site-a", Name: "Atelier A", Active: true}},
		Items: []catalog.Item{
			{ID: "active", Kind: catalog.KindService, Name: "Vidange", Price: catalog.Price{Kind: catalog.PriceFixed, Cents: 8990, TaxBasis: catalog.TaxInclusive, VATRate: 2000}},
			{ID: "expired", Kind: catalog.KindService, Name: "Ancien forfait", Price: catalog.Price{Kind: catalog.PriceQuote}, EffectiveTo: past},
		},
	}
	html := renderCatalog(t, catalog.List(page))
	for _, expected := range []string{"89,90", "TTC", "Tous les sites", "Échu", "L'agent ne l'annonce plus"} {
		if !strings.Contains(html, expected) {
			t.Errorf("catalog view is missing %q", expected)
		}
	}
}

func TestDestructiveImportPreviewRequiresExplicitConfirmation(t *testing.T) {
	page := catalog.Preview{
		Organization: "Garage Test", CanManage: true, Mode: catalog.ModeReplace,
		Import: catalog.Import{ID: "import-1", Filename: "catalog.csv", LocationName: "Atelier A", Status: catalog.ImportReady, Valid: 1},
		Rows:   []catalog.Row{{Number: 2, Status: catalog.RowValid, Name: "Vidange", Kind: catalog.KindService, Price: catalog.Price{Kind: catalog.PriceFixed, Cents: 5000, TaxBasis: catalog.TaxInclusive}}},
		Plan:   catalog.Plan{Create: 1, Remove: 3},
	}
	html := renderCatalog(t, catalog.PreviewPage(page))
	for _, expected := range []string{"3 lignes supprimées", `name="confirm"`, "required", "J'ai lu ce récapitulatif"} {
		if !strings.Contains(html, expected) {
			t.Errorf("replace preview is missing %q", expected)
		}
	}
}

func TestUploadCopyMatchesImplementedColumnRecognition(t *testing.T) {
	html := renderCatalog(t, catalog.UploadForm(catalog.Upload{
		Organization: "Garage Test", FieldErrors: map[string]string{},
		Locations: []catalog.LocationRef{{ID: "site-a", Name: "Atelier A", Active: true}},
	}))
	if !strings.Contains(html, "intitulés français et anglais courants sont reconnus") {
		t.Fatalf("upload guidance does not describe implemented recognition: %q", html)
	}
	if strings.Contains(html, "nommées librement") {
		t.Fatal("upload still promises an unimplemented arbitrary column mapper")
	}
}

func renderCatalog(t *testing.T, component templ.Component) string {
	t.Helper()
	var buffer bytes.Buffer
	if err := component.Render(context.Background(), &buffer); err != nil {
		t.Fatal(err)
	}
	return buffer.String()
}
