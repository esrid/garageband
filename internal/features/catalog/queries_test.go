package catalog_test

import (
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/esrid/garageband/internal/features/catalog"
	"github.com/esrid/garageband/internal/platform/db"
	"github.com/esrid/garageband/internal/platform/dbtest"
)

type catalogFixture struct {
	fixtures  *db.DB
	runtime   *db.DB
	store     *catalog.Store
	tenantID  string
	ownerID   string
	memberID  string
	locationA string
	locationB string
}

func TestCatalogCRUDIsLocationAwareAndManagerOnly(t *testing.T) {
	fixture := newCatalogFixture(t)
	all, err := fixture.store.Create(t.Context(), fixture.tenantID, fixture.ownerID, catalog.ItemInput{
		Kind: "labour_rate", Reference: "MO-01", Name: "Main-d’œuvre mécanique",
		PriceKind: "fixed", AmountCents: cents(8500), TaxBasis: "excl", VATBasisPoints: 2000,
		LocationScope: "all",
	})
	if err != nil {
		t.Fatal(err)
	}
	selected, err := fixture.store.Create(t.Context(), fixture.tenantID, fixture.ownerID, catalog.ItemInput{
		Kind: "service", Reference: "VID-01", Name: "Vidange",
		PriceKind: "from", AmountCents: cents(7900), TaxBasis: "incl", VATBasisPoints: 2000,
		DurationMinutes: minutes(45), LocationScope: "selected", LocationIDs: []string{fixture.locationB},
	})
	if err != nil {
		t.Fatal(err)
	}

	owner, err := fixture.store.List(t.Context(), fixture.tenantID, fixture.ownerID, catalog.CatalogFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if !owner.CanManage || len(owner.Items) != 2 || owner.Counts["service"] != 1 || owner.Counts["labour_rate"] != 1 {
		t.Fatalf("owner overview = %#v", owner)
	}
	member, err := fixture.store.List(t.Context(), fixture.tenantID, fixture.memberID, catalog.CatalogFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if member.CanManage || len(member.Items) != 1 || member.Items[0].ID != all.ID {
		t.Fatalf("member overview = %#v", member)
	}
	unassignedID := catalogInsertID(t, fixture.fixtures, `
		INSERT INTO users (provider, provider_id, email, name)
		VALUES ('test', 'catalog-unassigned', 'catalog-unassigned@example.com', 'Unassigned')
		RETURNING id::text`)
	catalogExec(t, fixture.fixtures, `
		INSERT INTO tenant_memberships (tenant_id, user_id, role)
		VALUES ($1, $2, 'member')`, fixture.tenantID, unassignedID)
	unassigned, err := fixture.store.List(t.Context(), fixture.tenantID, unassignedID, catalog.CatalogFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(unassigned.Items) != 0 || len(unassigned.Locations) != 0 {
		t.Fatalf("unassigned member sees catalog data: %#v", unassigned)
	}
	if _, err := fixture.store.Update(t.Context(), fixture.tenantID, fixture.memberID, all.ID, catalog.ItemInput{}); !errors.Is(err, catalog.ErrForbidden) {
		t.Fatalf("member update = %v", err)
	}
	if err := fixture.store.Archive(t.Context(), fixture.tenantID, fixture.ownerID, selected.ID); err != nil {
		t.Fatal(err)
	}
	owner, err = fixture.store.List(t.Context(), fixture.tenantID, fixture.ownerID, catalog.CatalogFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(owner.Items) != 1 || owner.Items[0].ID != all.ID {
		t.Fatalf("after archive = %#v", owner.Items)
	}
}

func TestQuotableNeverReturnsExpiredOrWrongLocationPrices(t *testing.T) {
	fixture := newCatalogFixture(t)
	if _, err := fixture.store.Create(t.Context(), fixture.tenantID, fixture.ownerID, catalog.ItemInput{
		Kind: "service", Reference: "ACTIVE", Name: "Diagnostic actif", PriceKind: "fixed",
		AmountCents: cents(4500), TaxBasis: "incl", VATBasisPoints: 2000,
		LocationScope: "selected", LocationIDs: []string{fixture.locationA},
	}); err != nil {
		t.Fatal(err)
	}
	past := mustDate(t, "2025-12-31")
	if _, err := fixture.store.Create(t.Context(), fixture.tenantID, fixture.ownerID, catalog.ItemInput{
		Kind: "service", Reference: "OLD", Name: "Diagnostic ancien", PriceKind: "fixed",
		AmountCents: cents(3000), TaxBasis: "incl", VATBasisPoints: 2000, EffectiveTo: &past,
		LocationScope: "selected", LocationIDs: []string{fixture.locationA},
	}); err != nil {
		t.Fatal(err)
	}
	items, err := fixture.store.Quotable(
		t.Context(), fixture.tenantID, fixture.memberID, fixture.locationA,
		"diagnostic", mustDate(t, "2026-08-01"),
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Reference != "ACTIVE" {
		t.Fatalf("quotable = %#v", items)
	}
	if _, err := fixture.store.Quotable(
		t.Context(), fixture.tenantID, fixture.memberID, fixture.locationB,
		"", mustDate(t, "2026-08-01"),
	); !errors.Is(err, catalog.ErrForbidden) {
		t.Fatalf("unassigned location quote = %v", err)
	}
}

func TestCatalogImportStagesPublishesAndRollsBack(t *testing.T) {
	fixture := newCatalogFixture(t)
	existing, err := fixture.store.Create(t.Context(), fixture.tenantID, fixture.ownerID, catalog.ItemInput{
		Kind: "service", Reference: "VID-01", Name: "Vidange",
		PriceKind: "fixed", AmountCents: cents(7000), TaxBasis: "incl", VATBasisPoints: 2000,
		DurationMinutes: minutes(45), LocationScope: "selected", LocationIDs: []string{fixture.locationA},
	})
	if err != nil {
		t.Fatal(err)
	}
	csv := []byte("Référence;Libellé;Type;Prix;Type de prix;TVA;Durée\n" +
		"VID-01;Vidange complète;Prestation;89,90;Fixe;20%;1h\n" +
		"FIL-01;Filtre à huile;Pièce;15,50;Fixe;20%;\n" +
		"BAD-01;Prix absent;Prestation;;Fixe;20%;30\n")
	imported, err := fixture.store.Upload(t.Context(), fixture.tenantID, fixture.ownerID, catalog.UploadInput{
		LocationID: fixture.locationA, Filename: "tarifs.csv", MediaType: "text/csv", Content: csv,
	})
	if err != nil {
		t.Fatal(err)
	}
	if imported.Status != "ready" || imported.ValidRows != 1 || imported.AmbiguousRows != 1 || imported.RejectedRows != 1 {
		t.Fatalf("staged import = %#v", imported)
	}
	if _, err := fixture.fixtures.Exec(`
		UPDATE catalog_imports SET source_filename = 'rewritten.csv'
		WHERE tenant_id = $1 AND id = $2`, fixture.tenantID, imported.ID); err == nil {
		t.Fatal("database unexpectedly allowed import audit evidence to change")
	}
	if err := fixture.runtime.WithinTenantUser(t.Context(), fixture.tenantID, fixture.ownerID, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(t.Context(), `
			DELETE FROM catalog_imports WHERE tenant_id = $1 AND id = $2`, fixture.tenantID, imported.ID)
		if err != nil {
			return err
		}
		deleted, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if deleted != 0 {
			t.Fatalf("runtime deleted %d import audit rows", deleted)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	preview, err := fixture.store.Import(t.Context(), fixture.tenantID, fixture.ownerID, imported.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(preview.Rows) != 3 || preview.Rows[0].MatchingItemID != existing.ID {
		t.Fatalf("preview = %#v", preview)
	}
	plan, err := fixture.store.Plan(t.Context(), fixture.tenantID, fixture.ownerID, imported.ID, "merge")
	if err != nil {
		t.Fatal(err)
	}
	if plan.Create != 1 || plan.Update != 1 || plan.Skip != 1 || plan.Remove != 0 {
		t.Fatalf("plan = %#v", plan)
	}
	publication, err := fixture.store.Publish(t.Context(), fixture.tenantID, fixture.ownerID, imported.ID, "merge")
	if err != nil {
		t.Fatal(err)
	}
	if publication.Version != 1 {
		t.Fatalf("publication = %#v", publication)
	}
	overview, err := fixture.store.List(t.Context(), fixture.tenantID, fixture.ownerID, catalog.CatalogFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(overview.Items) != 2 {
		t.Fatalf("published items = %#v", overview.Items)
	}
	updated := findItem(t, overview.Items, existing.ID)
	if updated.AmountCents == nil || *updated.AmountCents != 8990 || updated.Name != "Vidange complète" {
		t.Fatalf("updated item = %#v", updated)
	}
	if err := fixture.store.Rollback(t.Context(), fixture.tenantID, fixture.ownerID, publication.ID); err != nil {
		t.Fatal(err)
	}
	overview, err = fixture.store.List(t.Context(), fixture.tenantID, fixture.ownerID, catalog.CatalogFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(overview.Items) != 1 || overview.Items[0].ID != existing.ID ||
		overview.Items[0].AmountCents == nil || *overview.Items[0].AmountCents != 7000 {
		t.Fatalf("rolled back items = %#v", overview.Items)
	}
	duplicate, err := fixture.store.Upload(t.Context(), fixture.tenantID, fixture.ownerID, catalog.UploadInput{
		LocationID: fixture.locationA, Filename: "tarifs.csv", MediaType: "text/csv", Content: csv,
	})
	if !errors.Is(err, catalog.ErrDuplicateImport) || duplicate.ID != imported.ID {
		t.Fatalf("duplicate = %#v err %v", duplicate, err)
	}
}

func TestReplaceArchivesOnlySingleLocationItems(t *testing.T) {
	fixture := newCatalogFixture(t)
	obsolete, err := fixture.store.Create(t.Context(), fixture.tenantID, fixture.ownerID, catalog.ItemInput{
		Kind: "product", Reference: "OLD", Name: "Ancienne pièce", PriceKind: "fixed",
		AmountCents: cents(1000), TaxBasis: "incl", VATBasisPoints: 2000,
		LocationScope: "selected", LocationIDs: []string{fixture.locationA},
	})
	if err != nil {
		t.Fatal(err)
	}
	global, err := fixture.store.Create(t.Context(), fixture.tenantID, fixture.ownerID, catalog.ItemInput{
		Kind: "product", Reference: "GLOBAL", Name: "Produit national", PriceKind: "fixed",
		AmountCents: cents(2000), TaxBasis: "incl", VATBasisPoints: 2000, LocationScope: "all",
	})
	if err != nil {
		t.Fatal(err)
	}
	imported, err := fixture.store.Upload(t.Context(), fixture.tenantID, fixture.ownerID, catalog.UploadInput{
		LocationID: fixture.locationA, Filename: "replacement.csv", MediaType: "text/csv",
		Content: []byte("reference;name;type;price\nNEW;Nouvelle pièce;product;25\n"),
	})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := fixture.store.Plan(t.Context(), fixture.tenantID, fixture.ownerID, imported.ID, "replace")
	if err != nil {
		t.Fatal(err)
	}
	if plan.Create != 1 || plan.Remove != 1 {
		t.Fatalf("replace plan = %#v", plan)
	}
	if _, err := fixture.store.Publish(t.Context(), fixture.tenantID, fixture.ownerID, imported.ID, "replace"); err != nil {
		t.Fatal(err)
	}
	overview, err := fixture.store.List(t.Context(), fixture.tenantID, fixture.ownerID, catalog.CatalogFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(overview.Items) != 2 || hasItem(overview.Items, obsolete.ID) || !hasItem(overview.Items, global.ID) {
		t.Fatalf("replace result = %#v", overview.Items)
	}
}

func TestPostgreSQLRejectsInvalidScopeAndPrice(t *testing.T) {
	fixture := newCatalogFixture(t)
	err := fixture.storeDBTransaction(t, func(tx *sql.Tx) error {
		var itemID string
		if err := tx.QueryRowContext(t.Context(), `
			INSERT INTO catalog_items (
			    tenant_id, kind, name, price_kind, amount_cents,
			    tax_basis, vat_basis_points, location_scope,
			    created_by_user_id, updated_by_user_id
			) VALUES ($1, 'service', 'Invalide', 'quote', 1200,
			          'incl', 2000, 'selected', $2, $2)
			RETURNING id::text`, fixture.tenantID, fixture.ownerID).Scan(&itemID); err != nil {
			return err
		}
		_, err := tx.ExecContext(t.Context(), `
			INSERT INTO catalog_item_locations (tenant_id, catalog_item_id, location_id)
			VALUES ($1, $2, $3)`, fixture.tenantID, itemID, fixture.locationA)
		return err
	})
	if err == nil {
		t.Fatal("quote with an amount unexpectedly committed")
	}

	err = fixture.storeDBTransaction(t, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(t.Context(), `
			INSERT INTO catalog_items (
			    tenant_id, kind, name, price_kind, amount_cents,
			    tax_basis, vat_basis_points, location_scope,
			    created_by_user_id, updated_by_user_id
			) VALUES ($1, 'service', 'Sans site', 'fixed', 1200,
			          'incl', 2000, 'selected', $2, $2)`, fixture.tenantID, fixture.ownerID)
		return err
	})
	if err == nil {
		t.Fatal("selected scope without location unexpectedly committed")
	}
}

func newCatalogFixture(t *testing.T) catalogFixture {
	t.Helper()
	fixtures, runtime := dbtest.OpenRuntime(t)
	fixture := catalogFixture{fixtures: fixtures, runtime: runtime, store: catalog.NewStore(runtime)}
	fixture.ownerID = catalogInsertID(t, fixtures, `INSERT INTO users (provider, provider_id, email, name) VALUES ('test', 'catalog-owner', 'catalog-owner@example.com', 'Owner') RETURNING id::text`)
	fixture.memberID = catalogInsertID(t, fixtures, `INSERT INTO users (provider, provider_id, email, name) VALUES ('test', 'catalog-member', 'catalog-member@example.com', 'Member') RETURNING id::text`)
	fixture.tenantID = catalogInsertID(t, fixtures, `INSERT INTO tenants (slug, name) VALUES ('catalog-garage', 'Garage Catalogue') RETURNING id::text`)
	catalogExec(t, fixtures, `INSERT INTO tenant_memberships (tenant_id, user_id, role) VALUES ($1, $2, 'owner'), ($1, $3, 'member')`, fixture.tenantID, fixture.ownerID, fixture.memberID)
	fixture.locationA = catalogInsertID(t, fixtures, `INSERT INTO locations (tenant_id, slug, name, timezone) VALUES ($1, 'catalog-a', 'Atelier A', 'Europe/Paris') RETURNING id::text`, fixture.tenantID)
	fixture.locationB = catalogInsertID(t, fixtures, `INSERT INTO locations (tenant_id, slug, name, timezone) VALUES ($1, 'catalog-b', 'Atelier B', 'Europe/Paris') RETURNING id::text`, fixture.tenantID)
	catalogExec(t, fixtures, `INSERT INTO user_location_assignments (tenant_id, user_id, location_id, assigned_by_user_id) VALUES ($1, $2, $3, $4)`, fixture.tenantID, fixture.memberID, fixture.locationA, fixture.ownerID)
	return fixture
}

func (fixture catalogFixture) storeDBTransaction(t *testing.T, fn func(*sql.Tx) error) error {
	t.Helper()
	return fixture.storeWithin(t, fn)
}

func (fixture catalogFixture) storeWithin(t *testing.T, fn func(*sql.Tx) error) error {
	t.Helper()
	// Use the runtime role through the same transaction-scoped identity as the
	// application store, so FORCE RLS and deferred constraints are both tested.
	return fixture.runtime.WithinTenantUser(t.Context(), fixture.tenantID, fixture.ownerID, fn)
}

func catalogInsertID(t *testing.T, database *db.DB, query string, args ...any) string {
	t.Helper()
	var id string
	if err := database.QueryRow(query, args...).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func catalogExec(t *testing.T, database *db.DB, query string, args ...any) {
	t.Helper()
	if _, err := database.Exec(query, args...); err != nil {
		t.Fatal(err)
	}
}

func cents(value int64) *int64 { return &value }
func minutes(value int) *int   { return &value }

func mustDate(t *testing.T, value string) time.Time {
	t.Helper()
	parsed, err := time.Parse("2006-01-02", value)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}

func findItem(t *testing.T, items []catalog.CatalogItemRecord, id string) catalog.CatalogItemRecord {
	t.Helper()
	for _, item := range items {
		if item.ID == id {
			return item
		}
	}
	t.Fatalf("item %s not found in %#v", id, items)
	return catalog.CatalogItemRecord{}
}

func hasItem(items []catalog.CatalogItemRecord, id string) bool {
	for _, item := range items {
		if item.ID == id {
			return true
		}
	}
	return false
}
