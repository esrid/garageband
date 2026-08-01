package customers

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

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

	profile := Profile{
		Organization: "Garage Central", CanEdit: true,
		Customer: results[0],
		Vehicles: []ProfileVehicle{
			{ID: "v1", Plate: "AB-123-CD", Make: "Renault", Model: "Clio", Year: 2019, VIN: "VF1RJA00012345678"},
			{ID: "v2", Plate: "EF-456-GH", Make: "Peugeot", Model: "Partner", Year: 2022},
		},
		Timeline: []Event{
			{
				ID: "r1", Kind: EventRepair, At: time.Date(2026, 3, 12, 9, 0, 0, 0, time.UTC),
				Title: "Remplacement plaquettes et disques avant", VehicleLabel: "AB-123-CD",
				Status: "completed", LocationName: "Atelier Gerland", AuthoredHere: true,
				AmountCents: 148050, Currency: "EUR",
			},
			{
				ID: "a1", Kind: EventAppointment, At: time.Date(2026, 2, 28, 14, 30, 0, 0, time.UTC),
				Title: "Révision annuelle", VehicleLabel: "AB-123-CD",
				Status: "completed", LocationName: "Atelier Gerland", AuthoredHere: true,
			},
			{
				ID: "r2", Kind: EventRepair, At: time.Date(2025, 11, 4, 8, 0, 0, 0, time.UTC),
				VehicleLabel: "EF-456-GH", Status: "cancelled",
				LocationName: "Atelier Villeurbanne", AuthoredHere: false,
			},
		},
		Memories: []Memory{
			{Key: "Véhicule de courtoisie", Value: "En demande systématiquement", Status: "active", Confidence: 0.92},
			{Key: "Disponibilité", Value: "Préfère déposer le matin avant 9h", Status: "active", Confidence: 0.65},
			{Key: "Ancienne adresse", Value: "12 rue de la Part-Dieu", Status: "superseded"},
		},
	}
	shared := profile
	shared.CanEdit = false
	shared.Customer.Shared = true
	shared.Customer.HomeLocationName = "Atelier Vaise"

	pages := map[string]templ.Component{
		"customer-profile.html":        Show(profile),
		"customer-profile-shared.html": Show(shared),
		"customer-profile-empty.html": Show(Profile{
			Organization: "Garage Central", CanEdit: true,
			Customer: Customer{ID: "c9", LastName: "Nouveau", Phone: "+33400000000"},
		}),
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
