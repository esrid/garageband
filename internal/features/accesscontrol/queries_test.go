package accesscontrol_test

import (
	"database/sql"
	"errors"
	"slices"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/esrid/garageband/internal/features/accesscontrol"
	"github.com/esrid/garageband/internal/platform/db"
	"github.com/esrid/garageband/internal/platform/dbtest"
)

func TestAssignmentAndCustomerGrantLifecycle(t *testing.T) {
	database := dbtest.Open(t)
	ownerID := createUser(t, database, "access-owner@example.com")
	memberID := createUser(t, database, "access-member@example.com")
	tenantID := createTenant(t, database, ownerID)
	addMembership(t, database, tenantID, memberID, "member")
	homeLocationID := createLocation(t, database, tenantID, "home")
	receivingLocationID := createLocation(t, database, tenantID, "receiving")
	customerID := createCustomer(t, database, tenantID, homeLocationID)
	store := accesscontrol.NewStore(database)

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
	database := dbtest.Open(t)
	ownerID := createUser(t, database, "overview-owner@example.com")
	memberID := createUser(t, database, "overview-member@example.com")
	otherMemberID := createUser(t, database, "overview-other@example.com")
	tenantID := createTenant(t, database, ownerID)
	addMembership(t, database, tenantID, memberID, "manager")
	addMembership(t, database, tenantID, otherMemberID, "member")
	locationA := createLocation(t, database, tenantID, "overview-a")
	locationB := createLocation(t, database, tenantID, "overview-b")
	store := accesscontrol.NewStore(database)

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
