package team

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/a-h/templ"
)

// TestWritePreview dumps the team screen as static HTML for visual and
// accessibility review. It is a tool, not an assertion: it only runs when
// GARAGEBAND_PREVIEW_DIR points somewhere. The sample people below exist for
// this preview and for the view tests — never for production code.
func TestWritePreview(t *testing.T) {
	dir := os.Getenv("GARAGEBAND_PREVIEW_DIR")
	if dir == "" {
		t.Skip("set GARAGEBAND_PREVIEW_DIR to write preview pages")
	}

	locations := []LocationRef{
		{ID: "loc-a", Name: "Atelier Gerland", Active: true},
		{ID: "loc-b", Name: "Atelier Villeurbanne", Active: true},
		{ID: "loc-c", Name: "Atelier Vaise", Active: false},
	}
	owner := Member{
		UserID: "u1", Name: "Claire Dupont", Email: "claire@garage-central.fr",
		Role: RoleOwner,
	}
	manager := Member{
		UserID: "u2", Name: "Marc Leroy", Email: "marc@garage-central.fr",
		Role: RoleManager, LocationIDs: []string{"loc-a", "loc-c"},
	}
	newcomer := Member{
		UserID: "u3", Email: "nouveau@garage-central.fr", Role: RoleMember,
	}

	pages := map[string]templ.Component{
		"team.html": Index(Page{
			Organization: "Garage Central", CanManage: true, Locations: locations,
			Members: []Member{owner, manager, newcomer},
		}),
		"team-saved.html": Index(Page{
			Organization: "Garage Central", CanManage: true, Locations: locations,
			Notice:  Notice{Kind: NoticeSuccess, Message: "Les sites de Marc Leroy ont été mis à jour."},
			Members: []Member{owner, manager},
		}),
		"team-error.html": Index(Page{
			Organization: "Garage Central", CanManage: true, Locations: locations,
			Notice:  Notice{Kind: NoticeError, Message: "Les accès n'ont pas pu être chargés. Réessayez dans un instant."},
			Members: []Member{owner, manager},
		}),
		"team-readonly.html": Index(Page{
			Organization: "Garage Central", CanManage: false, Locations: locations,
			Members: []Member{owner, manager, newcomer},
		}),
		"team-no-locations.html": Index(Page{
			Organization: "Garage Central", CanManage: true,
			Members: []Member{owner, manager},
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
