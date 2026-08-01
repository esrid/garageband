package ui_test

import (
	"bytes"
	"context"
	"html"
	"strings"
	"testing"

	"github.com/esrid/garageband/internal/ui"
)

func render(t *testing.T, page ui.PageInfo) string {
	t.Helper()
	var buf bytes.Buffer
	if err := ui.Layout(page).Render(context.Background(), &buf); err != nil {
		t.Fatalf("render layout: %v", err)
	}
	return html.UnescapeString(buf.String())
}

// The signed-out and onboarding screens must not offer navigation into an app
// the visitor cannot reach yet.
func TestLayoutWithoutSectionRendersNoNavigation(t *testing.T) {
	page := render(t, ui.PageInfo{Title: "Connexion", NoIndex: true})
	for _, unwanted := range []string{
		"Navigation principale", "Tableau de bord", "/locations", "/logout",
	} {
		if strings.Contains(page, unwanted) {
			t.Errorf("bare layout should not contain %q", unwanted)
		}
	}
	if !strings.Contains(page, "Garageband") {
		t.Error("the brand should still be shown")
	}
}

func TestLayoutMarksTheCurrentSection(t *testing.T) {
	locations := render(t, ui.PageInfo{
		Title: "Sites du garage",
		Nav:   ui.Nav{Section: ui.SectionLocations, Workspace: "Garage Central", InWorkspace: true},
	})
	if !strings.Contains(locations, `href="/locations" aria-current="page"`) {
		t.Error("the locations link should be marked as the current page")
	}
	if !strings.Contains(locations, `href="/" aria-current="false"`) {
		t.Error("the dashboard link should be explicitly not current")
	}
	// Weight, not just the background colour, carries the current state.
	if !strings.Contains(locations, "border-base-content font-semibold") {
		t.Error("the current link should be emphasised beyond colour")
	}
	if !strings.Contains(locations, "Garage Central") {
		t.Error("the active workspace should be named in the shell")
	}

	dashboard := render(t, ui.PageInfo{
		Title: "Tableau de bord",
		Nav:   ui.Nav{Section: ui.SectionDashboard, Workspace: "Garage Central", InWorkspace: true},
	})
	if !strings.Contains(dashboard, `href="/" aria-current="page"`) {
		t.Error("the dashboard link should be marked as the current page")
	}
}

// /locations redirects back to the dashboard without an active workspace, so
// the shell must not offer the link in that state.
func TestLayoutHidesWorkspaceLinksWithoutAWorkspace(t *testing.T) {
	page := render(t, ui.PageInfo{
		Title: "Tableau de bord",
		Nav:   ui.Nav{Section: ui.SectionDashboard},
	})
	if strings.Contains(page, "/locations") {
		t.Error("the Sites link must be hidden until a workspace is active")
	}
	for _, want := range []string{"Navigation principale", "Tableau de bord", "/logout"} {
		if !strings.Contains(page, want) {
			t.Errorf("the shell is missing %q", want)
		}
	}
}

// A detail screen knows it is inside a workspace without knowing its name.
func TestLayoutLinksWorkspaceScreensWithoutAName(t *testing.T) {
	page := render(t, ui.PageInfo{
		Title: "Configurer le site",
		Nav:   ui.Nav{Section: ui.SectionLocations, InWorkspace: true},
	})
	if !strings.Contains(page, `href="/locations"`) {
		t.Error("the Sites link should be available on a workspace-scoped screen")
	}
}

func TestLayoutKeepsTheSeoSurface(t *testing.T) {
	page := render(t, ui.PageInfo{
		Title:       "Garageband",
		Description: "Agent téléphonique pour garages.",
		Canonical:   "https://garageband.fr/",
	})
	for _, want := range []string{
		"<title>Garageband</title>",
		`name="description" content="Agent téléphonique pour garages."`,
		`rel="canonical" href="https://garageband.fr/"`,
		`property="og:type" content="website"`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("layout is missing %q", want)
		}
	}
	if strings.Contains(page, "robots") {
		t.Error("a public page should not be marked noindex")
	}
}
