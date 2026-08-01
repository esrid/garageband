package locations

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/a-h/templ"
)

// TestWritePreview dumps the locations screens as static HTML for visual and
// accessibility review. It is a tool, not an assertion: it only runs when
// GARAGEBAND_PREVIEW_DIR points somewhere. The sample sites below exist for
// this preview and for the view tests — never for production code.
func TestWritePreview(t *testing.T) {
	dir := os.Getenv("GARAGEBAND_PREVIEW_DIR")
	if dir == "" {
		t.Skip("set GARAGEBAND_PREVIEW_DIR to write preview pages")
	}

	gerland := Location{
		ID:           "0198c0de-0000-7000-8000-000000000001",
		Name:         "Atelier Gerland",
		AddressLine1: "12 rue des Ateliers",
		PostalCode:   "69007", City: "Lyon", CountryCode: "FR",
		SIRET: "12345678900012", PhoneE164: "+33472000000",
		Email: "gerland@garage-central.fr", Timezone: "Europe/Paris",
		Status: StatusActive,
	}
	villeurbanne := Location{
		ID:           "0198c0de-0000-7000-8000-000000000002",
		Name:         "Atelier Villeurbanne",
		AddressLine1: "48 avenue Roger Salengro",
		AddressLine2: "Zone artisanale, bâtiment C",
		PostalCode:   "69100", City: "Villeurbanne", CountryCode: "FR",
		SIRET: "12345678900038", Email: "villeurbanne@garage-central.fr",
		Timezone: "Europe/Paris", Status: StatusActive,
	}
	vaise := Location{
		ID:           "0198c0de-0000-7000-8000-000000000003",
		Name:         "Atelier Vaise",
		AddressLine1: "3 quai Arloing",
		PostalCode:   "69009", City: "Lyon", CountryCode: "FR",
		Timezone: "Europe/Paris", Status: "inactive",
	}

	editable := Input{
		Name: gerland.Name, SIRET: gerland.SIRET,
		AddressLine1: gerland.AddressLine1, PostalCode: gerland.PostalCode,
		City: gerland.City, CountryCode: gerland.CountryCode,
		Timezone: gerland.Timezone, PhoneE164: gerland.PhoneE164,
		Email: gerland.Email, WebsiteURL: "https://garage-central.fr",
	}

	pages := map[string]templ.Component{
		"locations-one.html": Index(IndexPage{
			Organization: "Garage Central", CanManage: true,
			Locations: []Location{gerland},
		}),
		"locations-several.html": Index(IndexPage{
			Organization: "Garage Central", CanManage: true,
			Locations: []Location{gerland, villeurbanne},
		}),
		"locations-mixed.html": Index(IndexPage{
			Organization: "Garage Central", CanManage: true,
			Locations: []Location{gerland, villeurbanne, vaise},
		}),
		"locations-readonly.html": Index(IndexPage{
			Organization: "Garage Central", CanManage: false,
			Locations: []Location{gerland, vaise},
		}),
		"locations-error.html": Index(IndexPage{
			Organization: "Garage Central", CanManage: true,
			Notice: Notice{Kind: NoticeError, Message: "Les sites n'ont pas pu être chargés. Réessayez dans un instant."},
		}),
		"locations-saved.html": Index(IndexPage{
			Organization: "Garage Central", CanManage: true,
			Notice:    Notice{Kind: NoticeSuccess, Message: "Le site Atelier Villeurbanne a été ajouté."},
			Locations: []Location{gerland, villeurbanne},
		}),
		"location-new.html": Form(FormPage{
			CanManage: true,
			Values:    Input{CountryCode: "FR", Timezone: "Europe/Paris"},
		}),
		"location-edit.html": Form(FormPage{
			CanManage: true, ID: gerland.ID, Active: true, Values: editable,
		}),
		"location-closed.html": Form(FormPage{
			CanManage: true, ID: vaise.ID, Active: false,
			Values: Input{
				Name: vaise.Name, AddressLine1: vaise.AddressLine1,
				PostalCode: vaise.PostalCode, City: vaise.City,
				CountryCode: vaise.CountryCode, Timezone: vaise.Timezone,
			},
		}),
		"location-invalid.html": Form(FormPage{
			CanManage: true, ID: gerland.ID, Active: true,
			Notice: Notice{Kind: NoticeInvalid, Message: "Deux champs demandent une correction."},
			Values: Input{CountryCode: "FR", Timezone: "Europe/Paris"},
			FieldErrors: map[string]string{
				FieldName:       "Donnez un nom à ce site pour le reconnaître.",
				FieldPostalCode: "Le code postal doit contenir 5 chiffres.",
			},
		}),
		"location-readonly.html": Form(FormPage{
			CanManage: false, ID: gerland.ID, Active: true, Values: editable,
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
