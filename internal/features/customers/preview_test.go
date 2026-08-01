package customers

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/a-h/templ"
)

// TestWritePreview dumps the customer screen as static HTML for visual and
// accessibility review. It is a tool, not an assertion: it only runs when
// GARAGEBAND_PREVIEW_DIR points somewhere. The sample people below exist for
// this preview and for the view tests — never for production code.
func TestWritePreview(t *testing.T) {
	dir := os.Getenv("GARAGEBAND_PREVIEW_DIR")
	if dir == "" {
		t.Skip("set GARAGEBAND_PREVIEW_DIR to write preview pages")
	}

	results := []Customer{
		{
			ID: "c1", FirstName: "Claire", LastName: "Dupont",
			Phone: "+33472000000", Email: "claire.dupont@example.fr",
			Vehicles: []Vehicle{
				{Plate: "AB-123-CD", Model: "Renault Clio"},
				{Plate: "EF-456-GH", Model: "Peugeot Partner"},
			},
			HomeLocationName: "Atelier Gerland",
		},
		{
			ID: "c2", CompanyName: "Transports Martin", LastName: "Martin",
			Phone: "+33478000000",
			Vehicles: []Vehicle{
				{Plate: "AA-111-AA", Model: "Renault Master"},
				{Plate: "BB-222-BB", Model: "Renault Master"},
				{Plate: "CC-333-CC", Model: "Iveco Daily"},
				{Plate: "DD-444-DD"}, {Plate: "EE-555-EE"},
			},
			HomeLocationName: "Atelier Villeurbanne",
		},
		{
			ID: "c3", FirstName: "Yanis", LastName: "Benali",
			Email:            "yanis.benali@example.fr",
			HomeLocationName: "Atelier Vaise",
			Shared:           true,
		},
	}

	pages := map[string]templ.Component{
		"customers.html":         Index(Page{Organization: "Garage Central"}),
		"customers-results.html": Index(Page{Organization: "Garage Central", Query: "martin", Customers: results}),
		"customers-empty.html":   Index(Page{Organization: "Garage Central", Query: "ZZ-999-ZZ"}),
		"customers-error.html": Index(Page{
			Organization: "Garage Central",
			Notice:       Notice{Kind: NoticeError, Message: "La recherche est indisponible. Réessayez dans un instant."},
		}),
	}
	for name, page := range pages {
		file, err := os.Create(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		if err := page.Render(context.Background(), file); err != nil {
			t.Fatal(err)
		}
		if err := file.Close(); err != nil {
			t.Fatal(err)
		}
	}
}
