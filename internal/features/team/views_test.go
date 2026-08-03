package team

import (
	"bytes"
	"context"
	"html"
	"strings"
	"testing"

	"github.com/a-h/templ"
)

// render returns the page as plain text: templ escapes the apostrophes French
// copy is full of, so assertions would otherwise miss them.
func render(t *testing.T, page templ.Component) string {
	t.Helper()
	var buf bytes.Buffer
	if err := page.Render(context.Background(), &buf); err != nil {
		t.Fatalf("render: %v", err)
	}
	return html.UnescapeString(buf.String())
}

func mustContain(t *testing.T, page string, wants ...string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(page, want) {
			t.Errorf("page is missing %q", want)
		}
	}
}

func mustNotContain(t *testing.T, page string, unwanted ...string) {
	t.Helper()
	for _, bad := range unwanted {
		if strings.Contains(page, bad) {
			t.Errorf("page should not contain %q", bad)
		}
	}
}

var sites = []LocationRef{
	{ID: "loc-a", Name: "Atelier Gerland", Active: true},
	{ID: "loc-b", Name: "Atelier Villeurbanne", Active: true},
	{ID: "loc-c", Name: "Atelier Vaise", Active: false},
}

func TestOwnerAccessIsShownButNotEditable(t *testing.T) {
	page := render(t, Index(Page{
		Organization: "Garage Central", CanManage: true, Locations: sites,
		Members: []Member{{
			UserID: "u1", Name: "Claire Dupont", Email: "claire@garage-central.fr",
			Role: RoleOwner,
		}},
	}))
	mustContain(t, page,
		"Accès aux sites", "Claire Dupont", "claire@garage-central.fr",
		"Propriétaire", "Tous les sites, par son rôle", "changez le rôle",
	)
	// Offering checkboxes to a role that reaches everything would be a lie.
	// The assertion names this member's own form: the page also carries the
	// invitation form, whose site checkboxes are about someone else entirely.
	mustNotContain(t, page, "Modifier ses sites", `action="/team/u1/locations"`)
	// Nor is the owner someone this screen offers to remove.
	mustNotContain(t, page, `action="/team/u1/revoke"`)
}

func TestManagerAssignmentsArePreCheckedAndPostable(t *testing.T) {
	page := render(t, Index(Page{
		Organization: "Garage Central", CanManage: true, Locations: sites,
		Members: []Member{{
			UserID: "u2", Name: "Marc Leroy", Email: "marc@garage-central.fr",
			Role: RoleManager, LocationIDs: []string{"loc-a", "loc-c"},
		}},
	}))
	mustContain(t, page,
		"Responsable", "Modifier ses sites",
		`action="/team/u2/locations"`,
		`name="location_ids" value="loc-a"`,
		`name="location_ids" value="loc-b"`,
		`name="location_ids" value="loc-c"`,
		// The summary spells out the access instead of leaving it to the boxes.
		"Atelier Gerland, Atelier Vaise",
		// An inactive site is still assignable but says so.
		"Inactif",
	)
	if got := strings.Count(page, "checked"); got != 2 {
		t.Errorf("checked boxes = %d, want 2 (loc-a and loc-c)", got)
	}
}

func TestMemberWithoutAnySiteIsFlagged(t *testing.T) {
	page := render(t, Index(Page{
		Organization: "Garage Central", CanManage: true, Locations: sites,
		Members: []Member{
			{UserID: "u3", Email: "nouveau@garage-central.fr", Role: RoleMember},
		},
	}))
	mustContain(t, page,
		"1 personne n'a accès à aucun site",
		"peut se connecter mais ne voit aucun client",
		"Aucun site",
		// No display name yet: the email has to stand in as the label.
		"nouveau@garage-central.fr",
	)
}

func TestUnassignedHeadingAgreesInNumber(t *testing.T) {
	page := render(t, Index(Page{
		Organization: "Garage Central", CanManage: true, Locations: sites,
		Members: []Member{
			{UserID: "u3", Email: "a@example.fr", Role: RoleMember},
			{UserID: "u4", Email: "b@example.fr", Role: RoleManager},
			// An owner reaches everything, so they never count as unassigned.
			{UserID: "u5", Email: "c@example.fr", Role: RoleOwner},
		},
	}))
	mustContain(t, page, "2 personnes n'ont accès à aucun site")
	mustNotContain(t, page, "1 personne n'a accès")
}

func TestReadOnlyForSomeoneWhoCannotManage(t *testing.T) {
	page := render(t, Index(Page{
		Organization: "Garage Central", CanManage: false, Locations: sites,
		Members: []Member{{
			UserID: "u2", Name: "Marc Leroy", Role: RoleManager,
			LocationIDs: []string{"loc-a"},
		}},
	}))
	mustContain(t, page, "lecture seule", "Marc Leroy", "Atelier Gerland")
	mustNotContain(t, page, "Modifier ses sites", `name="location_ids"`, "Enregistrer ses sites")
}

func TestServerError(t *testing.T) {
	page := render(t, Index(Page{
		Organization: "Garage Central", CanManage: true, Locations: sites,
		Notice: Notice{Kind: NoticeError, Message: "Les accès n'ont pas pu être chargés."},
	}))
	mustContain(t, page,
		"Action impossible pour le moment",
		"Les accès n'ont pas pu être chargés.",
		"alert-warning",
	)
}

func TestSuccessNotice(t *testing.T) {
	page := render(t, Index(Page{
		Organization: "Garage Central", CanManage: true, Locations: sites,
		Notice: Notice{Kind: NoticeSuccess, Message: "Les sites de Marc Leroy ont été mis à jour."},
	}))
	mustContain(t, page, "C'est enregistré", "alert-success", "Marc Leroy")
}

// Assigning people to sites is meaningless before a site exists, and the
// screen should send the owner somewhere useful instead of showing empty forms.
func TestNoLocationsYet(t *testing.T) {
	page := render(t, Index(Page{
		Organization: "Garage Central", CanManage: true,
		Members: []Member{{UserID: "u2", Name: "Marc Leroy", Role: RoleManager}},
	}))
	mustContain(t, page, "Aucun site à attribuer", `href="/locations"`)
	mustNotContain(t, page, `name="location_ids"`)
}

func TestReadOnlyWithoutAssignedLocationDoesNotSuggestCreatingOne(t *testing.T) {
	page := render(t, Index(Page{
		Organization: "Garage Central", CanManage: false,
		Members: []Member{{UserID: "u2", Name: "Marc Leroy", Role: RoleManager}},
	}))
	mustContain(t, page, "Aucun site accessible", "Demandez à un propriétaire")
	mustNotContain(t, page, "Créez d'abord un site", ">Voir les sites</a>")
}

func TestMemberLabelFallsBackToEmail(t *testing.T) {
	named := Member{Name: "Claire Dupont", Email: "claire@example.fr"}
	if named.Label() != "Claire Dupont" {
		t.Errorf("Label() = %q", named.Label())
	}
	anonymous := Member{Email: "claire@example.fr"}
	if anonymous.Label() != "claire@example.fr" {
		t.Errorf("Label() = %q, want the email", anonymous.Label())
	}
}

func TestAccessSummaryFollowsTheDisplayedOrder(t *testing.T) {
	// Listed in the order the sites appear on screen, not the order the store
	// happened to return the assignments in.
	member := Member{Role: RoleManager, LocationIDs: []string{"loc-c", "loc-a"}}
	if got := accessSummary(member, sites); got != "Atelier Gerland, Atelier Vaise" {
		t.Errorf("accessSummary() = %q", got)
	}
	if got := accessSummary(Member{Role: RoleAdmin}, sites); got != "Tous les sites, par son rôle" {
		t.Errorf("admin summary = %q", got)
	}
	if got := accessSummary(Member{Role: RoleMember}, sites); got != "Aucun site" {
		t.Errorf("unassigned summary = %q", got)
	}
}
