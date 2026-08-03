package accesscontrol_test

import (
	"database/sql"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/esrid/garageband/internal/features/accesscontrol"
	"github.com/esrid/garageband/internal/platform/db"
	"github.com/esrid/garageband/internal/platform/dbtest"
)

func TestAssignmentAndCustomerGrantLifecycle(t *testing.T) {
	fixtures, runtime := dbtest.OpenRuntime(t)
	ownerID := createUser(t, fixtures, "access-owner@example.com")
	memberID := createUser(t, fixtures, "access-member@example.com")
	tenantID := createTenant(t, fixtures, ownerID)
	addMembership(t, fixtures, tenantID, memberID, "member")
	homeLocationID := createLocation(t, fixtures, tenantID, "home")
	receivingLocationID := createLocation(t, fixtures, tenantID, "receiving")
	customerID := createCustomer(t, fixtures, tenantID, homeLocationID)
	store := accesscontrol.NewStore(runtime)

	assignment, err := store.AssignLocation(
		t.Context(), tenantID, ownerID, memberID, receivingLocationID,
	)
	if err != nil {
		t.Fatal(err)
	}
	retried, err := store.AssignLocation(
		t.Context(), tenantID, ownerID, memberID, receivingLocationID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if retried.ID != assignment.ID {
		t.Fatalf("idempotent assignment created %q after %q", retried.ID, assignment.ID)
	}
	if _, err := store.AssignLocation(
		t.Context(), tenantID, memberID, memberID, homeLocationID,
	); !errors.Is(err, accesscontrol.ErrForbidden) {
		t.Fatalf("member assignment: got %v, want ErrForbidden", err)
	}
	revokedAssignment, err := store.RevokeLocationAssignment(
		t.Context(), tenantID, ownerID, assignment.ID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !revokedAssignment.RevokedAt.Valid || revokedAssignment.RevokedBy != ownerID {
		t.Fatalf("revoked assignment = %#v", revokedAssignment)
	}
	if _, err := store.RevokeLocationAssignment(
		t.Context(), tenantID, ownerID, assignment.ID,
	); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("second assignment revocation: got %v, want sql.ErrNoRows", err)
	}

	grant, err := store.GrantCustomer(
		t.Context(), tenantID, ownerID, customerID, receivingLocationID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if grant.SourceLocationID != homeLocationID {
		t.Fatalf("grant source = %q, want %q", grant.SourceLocationID, homeLocationID)
	}
	retriedGrant, err := store.GrantCustomer(
		t.Context(), tenantID, ownerID, customerID, receivingLocationID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if retriedGrant.ID != grant.ID {
		t.Fatalf("idempotent grant created %q after %q", retriedGrant.ID, grant.ID)
	}
	if _, err := store.GrantCustomer(
		t.Context(), tenantID, memberID, customerID, receivingLocationID,
	); !errors.Is(err, accesscontrol.ErrForbidden) {
		t.Fatalf("member grant: got %v, want ErrForbidden", err)
	}
	revokedGrant, err := store.RevokeCustomerGrant(
		t.Context(), tenantID, ownerID, grant.ID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !revokedGrant.RevokedAt.Valid || revokedGrant.RevokedBy != ownerID {
		t.Fatalf("revoked grant = %#v", revokedGrant)
	}
	regranted, err := store.GrantCustomer(
		t.Context(), tenantID, ownerID, customerID, receivingLocationID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if regranted.ID == grant.ID {
		t.Fatal("regrant unexpectedly reused revoked audit row")
	}
}

func TestTeamOverviewAndAtomicAssignmentReplacement(t *testing.T) {
	fixtures, runtime := dbtest.OpenRuntime(t)
	ownerID := createUser(t, fixtures, "overview-owner@example.com")
	memberID := createUser(t, fixtures, "overview-member@example.com")
	otherMemberID := createUser(t, fixtures, "overview-other@example.com")
	tenantID := createTenant(t, fixtures, ownerID)
	addMembership(t, fixtures, tenantID, memberID, "manager")
	addMembership(t, fixtures, tenantID, otherMemberID, "member")
	locationA := createLocation(t, fixtures, tenantID, "overview-a")
	locationB := createLocation(t, fixtures, tenantID, "overview-b")
	store := accesscontrol.NewStore(runtime)

	if err := store.ReplaceLocationAssignments(
		t.Context(), tenantID, ownerID, memberID,
		[]string{locationB, locationA, locationA},
	); err != nil {
		t.Fatal(err)
	}
	overview, err := store.TeamOverview(t.Context(), tenantID, ownerID)
	if err != nil {
		t.Fatal(err)
	}
	if !overview.CanManage || len(overview.Locations) != 2 || len(overview.Members) != 3 {
		t.Fatalf("owner overview = %#v", overview)
	}
	member := findMember(t, overview.Members, memberID)
	if !slices.Equal(member.LocationIDs, []string{locationA, locationB}) {
		t.Fatalf("member locations = %v", member.LocationIDs)
	}

	memberOverview, err := store.TeamOverview(t.Context(), tenantID, memberID)
	if err != nil {
		t.Fatal(err)
	}
	if memberOverview.CanManage || len(memberOverview.Members) != 1 ||
		memberOverview.Members[0].UserID != memberID {
		t.Fatalf("member overview = %#v", memberOverview)
	}

	missingLocation := "01980000-0000-7000-8000-000000000099"
	if err := store.ReplaceLocationAssignments(
		t.Context(), tenantID, ownerID, memberID,
		[]string{locationA, missingLocation},
	); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("invalid replacement = %v, want sql.ErrNoRows", err)
	}
	overview, err = store.TeamOverview(t.Context(), tenantID, ownerID)
	if err != nil {
		t.Fatal(err)
	}
	if got := findMember(t, overview.Members, memberID).LocationIDs; !slices.Equal(got, []string{locationA, locationB}) {
		t.Fatalf("failed replacement changed assignments to %v", got)
	}

	if err := store.ReplaceLocationAssignments(
		t.Context(), tenantID, ownerID, memberID, nil,
	); err != nil {
		t.Fatal(err)
	}
	overview, err = store.TeamOverview(t.Context(), tenantID, ownerID)
	if err != nil {
		t.Fatal(err)
	}
	if got := findMember(t, overview.Members, memberID).LocationIDs; len(got) != 0 {
		t.Fatalf("empty replacement left assignments %v", got)
	}

	if err := store.ReplaceLocationAssignments(
		t.Context(), tenantID, ownerID, ownerID, []string{locationA},
	); !errors.Is(err, accesscontrol.ErrForbidden) {
		t.Fatalf("owner target = %v, want ErrForbidden", err)
	}
}

func findMember(
	t *testing.T,
	members []accesscontrol.TeamMember,
	userID string,
) accesscontrol.TeamMember {
	t.Helper()
	for _, member := range members {
		if member.UserID == userID {
			return member
		}
	}
	t.Fatal("team member not found")
	return accesscontrol.TeamMember{}
}

func createUser(t *testing.T, database *db.DB, email string) string {
	t.Helper()
	var id string
	if err := database.QueryRow(t.Context(), `
		INSERT INTO users (provider, provider_id, email, name)
		VALUES ('test', $1, $1, 'Test User') RETURNING id`, email,
	).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func createTenant(t *testing.T, database *db.DB, ownerID string) string {
	t.Helper()
	var id string
	err := database.WithinNewTenantUser(t.Context(), ownerID, func(tx pgx.Tx, tenantID string) error {
		id = tenantID
		if _, err := tx.Exec(t.Context(), `
			INSERT INTO tenants (id, slug, name)
			VALUES ($1, 'access-control', 'Access Garage')`, tenantID); err != nil {
			return err
		}
		_, err := tx.Exec(t.Context(), `
			INSERT INTO tenant_memberships (tenant_id, user_id, role)
			VALUES ($1, $2, 'owner')`, tenantID, ownerID)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func addMembership(t *testing.T, database *db.DB, tenantID, userID, role string) {
	t.Helper()
	if _, err := database.Exec(t.Context(), `
		INSERT INTO tenant_memberships (tenant_id, user_id, role)
		VALUES ($1, $2, $3)`, tenantID, userID, role); err != nil {
		t.Fatal(err)
	}
}

func createLocation(t *testing.T, database *db.DB, tenantID, slug string) string {
	t.Helper()
	var id string
	if err := database.QueryRow(t.Context(), `
		INSERT INTO locations (tenant_id, slug, name)
		VALUES ($1, $2, $2) RETURNING id`, tenantID, slug,
	).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func createCustomer(t *testing.T, database *db.DB, tenantID, locationID string) string {
	t.Helper()
	var id string
	if err := database.QueryRow(t.Context(), `
		INSERT INTO customers (
			tenant_id, home_location_id, first_name, last_name
		) VALUES ($1, $2, 'Alice', 'Martin') RETURNING id`,
		tenantID, locationID,
	).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

// TestStaffInviteAndRevokeLifecycle walks the path a garage owner actually
// takes: enrol the mechanic by name alone, hand over a link, then take the
// access back when they leave.
func TestStaffInviteAndRevokeLifecycle(t *testing.T) {
	fixtures, runtime := dbtest.OpenRuntime(t)
	ownerID := createUser(t, fixtures, "invite-owner@example.com")
	memberID := createUser(t, fixtures, "invite-member@example.com")
	tenantID := createTenant(t, fixtures, ownerID)
	addMembership(t, fixtures, tenantID, memberID, "member")
	locationID := createLocation(t, fixtures, tenantID, "workshop")
	store := accesscontrol.NewStore(runtime)

	if _, err := store.InviteStaff(
		t.Context(), tenantID, ownerID, "   ", []string{locationID},
	); !errors.Is(err, accesscontrol.ErrNameRequired) {
		t.Fatalf("blank name = %v, want ErrNameRequired", err)
	}
	if _, err := store.InviteStaff(
		t.Context(), tenantID, memberID, "Intrus", nil,
	); !errors.Is(err, accesscontrol.ErrForbidden) {
		t.Fatalf("member invite = %v, want ErrForbidden", err)
	}

	invite, err := store.InviteStaff(
		t.Context(), tenantID, ownerID, "Karim Mécano", []string{locationID},
	)
	if err != nil {
		t.Fatal(err)
	}
	if invite.Token == "" || invite.UserID == "" {
		t.Fatalf("invite = %+v, want a token and a user", invite)
	}

	// The raw token must never be what the database holds.
	var stored string
	if err := fixtures.QueryRow(t.Context(),
		`SELECT token_hash FROM staff_invites WHERE user_id = $1`, invite.UserID,
	).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored == invite.Token {
		t.Fatal("staff_invites stores the raw token")
	}
	if stored != accesscontrol.HashToken(invite.Token) {
		t.Fatal("staff_invites does not store the token's hash")
	}

	overview, err := store.TeamOverview(t.Context(), tenantID, ownerID)
	if err != nil {
		t.Fatal(err)
	}
	invited := findMember(t, overview.Members, invite.UserID)
	if invited.Name != "Karim Mécano" || invited.Role != "member" {
		t.Fatalf("invited member = %+v, want a named member", invited)
	}
	if invited.InviteState != accesscontrol.InvitePending {
		t.Fatalf("invite state = %q, want %q", invited.InviteState, accesscontrol.InvitePending)
	}
	if !slices.Equal(invited.LocationIDs, []string{locationID}) {
		t.Fatalf("invited locations = %v, want %v", invited.LocationIDs, []string{locationID})
	}

	// An owner cannot remove themselves, and cannot be removed by an admin
	// either: this screen changes staff, not who runs the business.
	if err := store.RevokeStaff(
		t.Context(), tenantID, ownerID, ownerID,
	); !errors.Is(err, accesscontrol.ErrForbidden) {
		t.Fatalf("self revoke = %v, want ErrForbidden", err)
	}
	adminID := createUser(t, fixtures, "invite-admin@example.com")
	addMembership(t, fixtures, tenantID, adminID, "admin")
	if err := store.RevokeStaff(
		t.Context(), tenantID, adminID, ownerID,
	); !errors.Is(err, accesscontrol.ErrForbidden) {
		t.Fatalf("admin removing the owner = %v, want ErrForbidden", err)
	}
	if _, err := store.TeamOverview(t.Context(), tenantID, ownerID); err != nil {
		t.Fatalf("owner lost their own access: %v", err)
	}

	if err := store.RevokeStaff(t.Context(), tenantID, memberID, invite.UserID); !errors.Is(
		err, accesscontrol.ErrForbidden,
	) {
		t.Fatalf("member revoke = %v, want ErrForbidden", err)
	}
	if err := store.RevokeStaff(t.Context(), tenantID, ownerID, invite.UserID); err != nil {
		t.Fatal(err)
	}

	// The pending link must die with the membership, not outlive it.
	var invitesLeft int
	if err := fixtures.QueryRow(t.Context(),
		`SELECT COUNT(*) FROM staff_invites WHERE user_id = $1`, invite.UserID,
	).Scan(&invitesLeft); err != nil {
		t.Fatal(err)
	}
	if invitesLeft != 0 {
		t.Fatalf("%d invitations survived the revocation, want 0", invitesLeft)
	}
}

// TestInviteCodeIsShortAndReissuable covers what a garage actually needs: a
// code short enough to read out loud, and a way to hand someone a new one when
// they sit down at a second computer.
func TestInviteCodeIsShortAndReissuable(t *testing.T) {
	fixtures, runtime := dbtest.OpenRuntime(t)
	ownerID := createUser(t, fixtures, "code-owner@example.com")
	tenantID := createTenant(t, fixtures, ownerID)
	locationID := createLocation(t, fixtures, tenantID, "code-workshop")
	store := accesscontrol.NewStore(runtime)

	first, err := store.InviteStaff(
		t.Context(), tenantID, ownerID, "Sophie Accueil", []string{locationID},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Token) != 12 {
		t.Fatalf("code %q is %d characters, want 12", first.Token, len(first.Token))
	}
	for _, r := range first.Token {
		if !((r >= 'A' && r <= 'Z') || (r >= '2' && r <= '7')) {
			t.Fatalf("code %q contains %q, outside the unambiguous base32 alphabet", first.Token, r)
		}
	}

	// What the employee types is never exactly what the screen shows.
	for _, typed := range []string{
		first.Token,
		strings.ToLower(first.Token),
		first.Token[:4] + "-" + first.Token[4:8] + "-" + first.Token[8:],
		" " + strings.ToLower(first.Token[:4]) + " " + first.Token[4:] + " ",
	} {
		if got := accesscontrol.NormalizeInviteCode(typed); got != first.Token {
			t.Fatalf("normalizing %q gave %q, want %q", typed, got, first.Token)
		}
	}

	second, err := store.ReissueInvite(t.Context(), tenantID, ownerID, first.UserID)
	if err != nil {
		t.Fatal(err)
	}
	if second.Token == first.Token {
		t.Fatal("reissuing handed back the same code")
	}

	// Exactly one code is live at a time: the replaced one is gone, not merely
	// superseded.
	var live int
	if err := fixtures.QueryRow(t.Context(), `
		SELECT COUNT(*) FROM staff_invites
		WHERE user_id = $1 AND accepted_at IS NULL`, first.UserID,
	).Scan(&live); err != nil {
		t.Fatal(err)
	}
	if live != 1 {
		t.Fatalf("%d live codes after reissuing, want 1", live)
	}
	var survives bool
	if err := fixtures.QueryRow(t.Context(), `
		SELECT EXISTS (SELECT 1 FROM staff_invites WHERE token_hash = $1)`,
		accesscontrol.HashToken(first.Token),
	).Scan(&survives); err != nil {
		t.Fatal(err)
	}
	if survives {
		t.Fatal("the replaced code still works")
	}

	// The owner signs in with Google; handing them a staff code would be a way
	// to take their seat.
	if _, err := store.ReissueInvite(
		t.Context(), tenantID, ownerID, ownerID,
	); !errors.Is(err, accesscontrol.ErrForbidden) {
		t.Fatalf("reissuing for the owner = %v, want ErrForbidden", err)
	}
}

// TestRenameStaffFixesTyposWithoutCostingAccess pins the cheap cure for the
// mistake an owner makes most: a name typed wrong. It must not cost the person
// their sites, their code, or their session.
func TestRenameStaffFixesTyposWithoutCostingAccess(t *testing.T) {
	fixtures, runtime := dbtest.OpenRuntime(t)
	ownerID := createUser(t, fixtures, "rename-owner@example.com")
	memberID := createUser(t, fixtures, "rename-member@example.com")
	tenantID := createTenant(t, fixtures, ownerID)
	addMembership(t, fixtures, tenantID, memberID, "member")
	locationID := createLocation(t, fixtures, tenantID, "rename-workshop")
	store := accesscontrol.NewStore(runtime)

	invite, err := store.InviteStaff(
		t.Context(), tenantID, ownerID, "acceuil", []string{locationID},
	)
	if err != nil {
		t.Fatal(err)
	}

	if err := store.RenameStaff(
		t.Context(), tenantID, ownerID, invite.UserID, "   ",
	); !errors.Is(err, accesscontrol.ErrNameRequired) {
		t.Fatalf("blank rename = %v, want ErrNameRequired", err)
	}
	if err := store.RenameStaff(
		t.Context(), tenantID, memberID, invite.UserID, "Pirate",
	); !errors.Is(err, accesscontrol.ErrForbidden) {
		t.Fatalf("member rename = %v, want ErrForbidden", err)
	}
	// The owner signs in through Google, which rewrites their name at every
	// login: editing it here would quietly revert.
	if err := store.RenameStaff(
		t.Context(), tenantID, ownerID, ownerID, "Le Patron",
	); !errors.Is(err, accesscontrol.ErrForbidden) {
		t.Fatalf("renaming the owner = %v, want ErrForbidden", err)
	}

	if err := store.RenameStaff(
		t.Context(), tenantID, ownerID, invite.UserID, "  Accueil  ",
	); err != nil {
		t.Fatal(err)
	}

	overview, err := store.TeamOverview(t.Context(), tenantID, ownerID)
	if err != nil {
		t.Fatal(err)
	}
	renamed := findMember(t, overview.Members, invite.UserID)
	if renamed.Name != "Accueil" {
		t.Fatalf("name = %q, want %q", renamed.Name, "Accueil")
	}
	// Everything the person had must survive the correction.
	if !slices.Equal(renamed.LocationIDs, []string{locationID}) {
		t.Fatalf("sites after rename = %v, want %v", renamed.LocationIDs, []string{locationID})
	}
	if renamed.InviteState != accesscontrol.InvitePending {
		t.Fatalf("invite state after rename = %q, want it still pending", renamed.InviteState)
	}
	var codeSurvives bool
	if err := fixtures.QueryRow(t.Context(), `
		SELECT EXISTS (SELECT 1 FROM staff_invites WHERE token_hash = $1)`,
		accesscontrol.HashToken(invite.Token),
	).Scan(&codeSurvives); err != nil {
		t.Fatal(err)
	}
	if !codeSurvives {
		t.Fatal("the rename invalidated the code the person was already given")
	}
}
