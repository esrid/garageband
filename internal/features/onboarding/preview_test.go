package onboarding

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/a-h/templ"
)

// TestWritePreview dumps the onboarding pages as static HTML for visual and
// accessibility review. It is a tool, not an assertion: it only runs when
// GARAGEBAND_PREVIEW_DIR points somewhere.
func TestWritePreview(t *testing.T) {
	dir := os.Getenv("GARAGEBAND_PREVIEW_DIR")
	if dir == "" {
		t.Skip("set GARAGEBAND_PREVIEW_DIR to write preview pages")
	}
	filled := formData{
		DraftID: "0198c0de-0000-7000-8000-000000000001", Name: "Garage Central",
		LegalName: "CENTRAL AUTOMOBILES SAS", Slug: "garage-central",
		SIRET: "12345678900012", LocationName: "Atelier Gerland",
		AddressLine1: "12 rue des Ateliers", PostalCode: "69007", City: "Lyon",
		CountryCode: "FR", WebsiteURL: "https://garage-central.fr",
	}
	withComplement := filled
	withComplement.AddressLine2 = "Zone artisanale, bâtiment C"

	pages := map[string]templ.Component{
		"lookup.html":             LookupPage("", "", ""),
		"lookup-notfound.html":    LookupPage("12345678900012", "Aucun établissement actif ne correspond à ce SIRET.", noticeNotFound),
		"lookup-unavailable.html": LookupPage("12345678900012", "Le registre officiel est momentanément indisponible. Réessayez.", noticeKindForLookupStatus(http.StatusBadGateway)),
		"confirm.html":            ConfirmationPage(filled),
		"confirm-complement.html": ConfirmationPage(withComplement),
		"confirm-expired.html":    ConfirmationPage(withError(filled, "Votre recherche est trop ancienne pour être utilisée. Relancez la recherche du SIRET, cela ne prend qu'un instant.", noticeExpired)),
		"confirm-duplicate.html":  ConfirmationPage(withError(filled, "Cet identifiant d'espace est déjà pris. Choisissez-en un autre ci-dessous.", noticeDuplicate)),
		"confirm-invalid.html":    ConfirmationPage(withError(filled, "L'identifiant d'espace doit comporter 2 à 63 lettres minuscules, chiffres ou tirets.", noticeInvalid)),
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

func withError(data formData, message string, kind string) formData {
	data.Error, data.ErrorKind = message, kind
	return data
}
