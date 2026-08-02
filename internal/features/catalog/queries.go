package catalog

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/esrid/garageband/internal/platform/db"
)

type Store struct{ db *db.DB }

func NewStore(database *db.DB) *Store { return &Store{db: database} }

func (s *Store) List(
	ctx context.Context,
	tenantID string,
	userID string,
	filter CatalogFilter,
) (overview CatalogOverview, err error) {
	err = s.db.WithinTenantUser(ctx, tenantID, userID, func(tx pgx.Tx) error {
		role, err := loadCatalogPrincipal(ctx, tx, tenantID, userID, &overview.Organization)
		if err != nil {
			return err
		}
		overview.CanManage = role == "owner" || role == "admin"
		overview.Counts = make(map[string]int)
		if overview.Locations, err = loadCatalogLocations(ctx, tx, tenantID); err != nil {
			return err
		}

		rows, err := tx.Query(ctx, `
			SELECT kind, count(*)
			FROM catalog_items
			WHERE tenant_id = $1 AND archived_at IS NULL
			GROUP BY kind`, tenantID)
		if err != nil {
			return err
		}
		for rows.Next() {
			var kind string
			var count int
			if err := rows.Scan(&kind, &count); err != nil {
				rows.Close()
				return err
			}
			overview.Counts[kind] = count
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}

		itemRows, err := tx.Query(ctx, catalogItemSelect+`
			WHERE item.tenant_id = $1
			  AND item.archived_at IS NULL
			  AND ($2 = '' OR item.kind = $2)
			  AND ($3 = '' OR strpos(
			        lower(item.name || ' ' || COALESCE(item.reference, '') || ' ' || COALESCE(item.description, '')),
			        lower($3)
			      ) > 0)
			ORDER BY item.name, item.id`, tenantID, strings.TrimSpace(filter.Kind), strings.TrimSpace(filter.Query))
		if err != nil {
			return err
		}
		overview.Items, err = scanCatalogItems(itemRows)
		return err
	})
	return overview, err
}

func (s *Store) Item(
	ctx context.Context,
	tenantID string,
	userID string,
	itemID string,
) (item CatalogItemRecord, organization string, canManage bool, locations []CatalogLocation, err error) {
	err = s.db.WithinTenantUser(ctx, tenantID, userID, func(tx pgx.Tx) error {
		role, err := loadCatalogPrincipal(ctx, tx, tenantID, userID, &organization)
		if err != nil {
			return err
		}
		canManage = role == "owner" || role == "admin"
		if locations, err = loadCatalogLocations(ctx, tx, tenantID); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, catalogItemSelect+`
			WHERE item.tenant_id = $1 AND item.id = $2 AND item.archived_at IS NULL`, tenantID, itemID)
		if err != nil {
			return err
		}
		items, err := scanCatalogItems(rows)
		if err != nil {
			return err
		}
		if len(items) == 0 {
			return sql.ErrNoRows
		}
		item = items[0]
		return nil
	})
	return
}

// Quotable returns only prices that the agent may state at the selected
// location and date. The future telephone and internal-chat tools can share
// this rule without importing one another.
func (s *Store) Quotable(
	ctx context.Context,
	tenantID string,
	userID string,
	locationID string,
	query string,
	at time.Time,
) (items []CatalogItemRecord, err error) {
	err = s.db.WithinTenantUser(ctx, tenantID, userID, func(tx pgx.Tx) error {
		if _, err := loadCatalogPrincipal(ctx, tx, tenantID, userID, nil); err != nil {
			return err
		}
		var accessible bool
		if err := tx.QueryRow(ctx, `
			SELECT EXISTS (
			    SELECT 1 FROM locations
			    WHERE tenant_id = $1 AND id = $2 AND status = 'active'
			)`, tenantID, locationID).Scan(&accessible); err != nil {
			return err
		}
		if !accessible {
			return ErrForbidden
		}
		day := at
		if day.IsZero() {
			day = time.Now().UTC()
		}
		rows, err := tx.Query(ctx, catalogItemSelect+`
			WHERE item.tenant_id = $1 AND item.archived_at IS NULL
			  AND (item.effective_from IS NULL OR item.effective_from <= $3::date)
			  AND (item.effective_to IS NULL OR item.effective_to >= $3::date)
			  AND (
			      item.location_scope = 'all'
			      OR EXISTS (
			          SELECT 1 FROM catalog_item_locations il
			          WHERE il.tenant_id = item.tenant_id
			            AND il.catalog_item_id = item.id AND il.location_id = $2
			      )
			  )
			  AND ($4 = '' OR strpos(
			      lower(item.name || ' ' || COALESCE(item.reference, '') || ' ' || COALESCE(item.description, '')),
			      lower($4)
			  ) > 0)
			ORDER BY item.name, item.id
			LIMIT 20`, tenantID, locationID, day, strings.TrimSpace(query))
		if err != nil {
			return err
		}
		items, err = scanCatalogItems(rows)
		return err
	})
	return items, err
}

func (s *Store) NewItemContext(
	ctx context.Context,
	tenantID string,
	userID string,
) (organization string, canManage bool, locations []CatalogLocation, err error) {
	err = s.db.WithinTenantUser(ctx, tenantID, userID, func(tx pgx.Tx) error {
		role, err := loadCatalogPrincipal(ctx, tx, tenantID, userID, &organization)
		if err != nil {
			return err
		}
		canManage = role == "owner" || role == "admin"
		locations, err = loadCatalogLocations(ctx, tx, tenantID)
		return err
	})
	return
}

func (s *Store) Create(
	ctx context.Context,
	tenantID string,
	userID string,
	input ItemInput,
) (item CatalogItemRecord, err error) {
	err = s.db.WithinTenantUser(ctx, tenantID, userID, func(tx pgx.Tx) error {
		if err := requireCatalogManager(ctx, tx); err != nil {
			return err
		}
		locations, err := validateCatalogLocations(ctx, tx, tenantID, input.LocationScope, input.LocationIDs)
		if err != nil {
			return err
		}
		itemID, err := insertCatalogItem(ctx, tx, tenantID, userID, "", input)
		if err != nil {
			return mapCatalogWriteError(err)
		}
		if err := replaceCatalogItemLocations(ctx, tx, tenantID, itemID, locations); err != nil {
			return err
		}
		loaded, err := loadCatalogItemsByID(ctx, tx, tenantID, []string{itemID})
		if err != nil {
			return err
		}
		item = loaded[0]
		return nil
	})
	return item, err
}

func (s *Store) Update(
	ctx context.Context,
	tenantID string,
	userID string,
	itemID string,
	input ItemInput,
) (item CatalogItemRecord, err error) {
	err = s.db.WithinTenantUser(ctx, tenantID, userID, func(tx pgx.Tx) error {
		if err := requireCatalogManager(ctx, tx); err != nil {
			return err
		}
		locations, err := validateCatalogLocations(ctx, tx, tenantID, input.LocationScope, input.LocationIDs)
		if err != nil {
			return err
		}
		result, err := tx.Exec(ctx, `
			UPDATE catalog_items SET
			    kind = $3, reference = NULLIF($4, ''), name = $5,
			    description = NULLIF($6, ''), price_kind = $7,
			    amount_cents = $8, max_amount_cents = $9,
			    tax_basis = $10, vat_basis_points = $11,
			    per_hour = ($3 = 'labour_rate'), duration_minutes = $12,
			    effective_from = $13, effective_to = $14,
			    location_scope = $15, updated_by_user_id = $2
			WHERE tenant_id = $1 AND id = $16 AND archived_at IS NULL`,
			tenantID, userID, input.Kind, strings.TrimSpace(input.Reference), strings.TrimSpace(input.Name),
			strings.TrimSpace(input.Description), input.PriceKind, input.AmountCents, input.MaxAmountCents,
			input.TaxBasis, input.VATBasisPoints, input.DurationMinutes, input.EffectiveFrom,
			input.EffectiveTo, input.LocationScope, itemID)
		if err != nil {
			return mapCatalogWriteError(err)
		}
		if result.RowsAffected() != 1 {
			return sql.ErrNoRows
		}
		if err := replaceCatalogItemLocations(ctx, tx, tenantID, itemID, locations); err != nil {
			return err
		}
		loaded, err := loadCatalogItemsByID(ctx, tx, tenantID, []string{itemID})
		if err != nil {
			return err
		}
		item = loaded[0]
		return nil
	})
	return item, err
}

func (s *Store) Archive(ctx context.Context, tenantID, userID, itemID string) error {
	return s.db.WithinTenantUser(ctx, tenantID, userID, func(tx pgx.Tx) error {
		if err := requireCatalogManager(ctx, tx); err != nil {
			return err
		}
		result, err := tx.Exec(ctx, `
			UPDATE catalog_items
			SET archived_at = now(), archived_by_user_id = $2, updated_by_user_id = $2
			WHERE tenant_id = $1 AND id = $3 AND archived_at IS NULL`, tenantID, userID, itemID)
		if err != nil {
			return err
		}
		if result.RowsAffected() != 1 {
			return sql.ErrNoRows
		}
		return nil
	})
}

func (s *Store) Upload(
	ctx context.Context,
	tenantID string,
	userID string,
	input UploadInput,
) (record ImportRecord, err error) {
	size := input.Size
	if size == 0 {
		size = int64(len(input.Content))
	}
	checksum := sha256.Sum256(input.Content)
	format := uploadFormat(input.Filename)
	var parsed []parsedRow
	var rejection string
	content := input.Content
	if size > MaxUploadBytes || len(content) > MaxUploadBytes {
		rejection, content = "too_large", nil
	} else {
		format, parsed, rejection = parseUpload(input.Filename, input.Content)
	}
	if strings.TrimSpace(input.MediaType) == "" {
		input.MediaType = "application/octet-stream"
	}

	err = s.db.WithinTenantUser(ctx, tenantID, userID, func(tx pgx.Tx) error {
		if err := requireCatalogManager(ctx, tx); err != nil {
			return err
		}
		if _, err := validateCatalogLocations(ctx, tx, tenantID, "selected", []string{input.LocationID}); err != nil {
			return err
		}
		status := "analyzing"
		var rejected any
		if rejection != "" {
			status, rejected = "rejected", rejection
		}
		var id string
		err := tx.QueryRow(ctx, `
			INSERT INTO catalog_imports (
			    tenant_id, location_id, uploaded_by_user_id,
			    source_filename, source_format, source_media_type,
			    source_size_bytes, source_sha256, source_content,
			    status, rejection_reason
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
			ON CONFLICT (tenant_id, location_id, source_sha256) DO NOTHING
			RETURNING id::text`, tenantID, input.LocationID, userID,
			strings.TrimSpace(input.Filename), format, input.MediaType,
			size, checksum[:], content, status, rejected).Scan(&id)
		if errors.Is(err, sql.ErrNoRows) {
			if loadErr := tx.QueryRow(ctx, `
				SELECT id::text FROM catalog_imports
				WHERE tenant_id = $1 AND location_id = $2 AND source_sha256 = $3`,
				tenantID, input.LocationID, checksum[:]).Scan(&id); loadErr != nil {
				return loadErr
			}
			loaded, loadErr := loadImport(ctx, tx, tenantID, id)
			if loadErr != nil {
				return loadErr
			}
			record = loaded
			return ErrDuplicateImport
		}
		if err != nil {
			return err
		}
		if rejection == "" {
			if err := classifyAndInsertRows(ctx, tx, tenantID, input.LocationID, id, parsed); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `
				UPDATE catalog_imports SET status = 'ready'
				WHERE tenant_id = $1 AND id = $2 AND status = 'analyzing'`, tenantID, id); err != nil {
				return err
			}
		}
		loaded, err := loadImport(ctx, tx, tenantID, id)
		if err != nil {
			return err
		}
		record = loaded
		return nil
	})
	return record, err
}

func (s *Store) Imports(ctx context.Context, tenantID, userID string) (overview ImportsOverview, err error) {
	err = s.db.WithinTenantUser(ctx, tenantID, userID, func(tx pgx.Tx) error {
		role, err := loadCatalogPrincipal(ctx, tx, tenantID, userID, &overview.Organization)
		if err != nil {
			return err
		}
		overview.CanManage = role == "owner" || role == "admin"
		rows, err := tx.Query(ctx, importSelect+`
			WHERE catalog_import.tenant_id = $1
			GROUP BY catalog_import.id, location.name, uploader.name, uploader.email
			ORDER BY catalog_import.created_at DESC, catalog_import.id DESC`, tenantID)
		if err != nil {
			return err
		}
		overview.Imports, err = scanImports(rows)
		return err
	})
	return overview, err
}

func (s *Store) Import(ctx context.Context, tenantID, userID, importID string) (preview ImportPreview, err error) {
	err = s.db.WithinTenantUser(ctx, tenantID, userID, func(tx pgx.Tx) error {
		role, err := loadCatalogPrincipal(ctx, tx, tenantID, userID, &preview.Organization)
		if err != nil {
			return err
		}
		preview.CanManage = role == "owner" || role == "admin"
		if preview.Import, err = loadImport(ctx, tx, tenantID, importID); err != nil {
			return err
		}
		preview.Rows, err = loadImportRows(ctx, tx, tenantID, importID)
		return err
	})
	return preview, err
}

func (s *Store) Plan(ctx context.Context, tenantID, userID, importID, mode string) (plan PublishPlan, err error) {
	err = s.db.WithinTenantUser(ctx, tenantID, userID, func(tx pgx.Tx) error {
		if _, err := loadCatalogPrincipal(ctx, tx, tenantID, userID, nil); err != nil {
			return err
		}
		plan, err = calculatePublishPlan(ctx, tx, tenantID, importID, mode)
		return err
	})
	return plan, err
}

func (s *Store) Publish(
	ctx context.Context,
	tenantID string,
	userID string,
	importID string,
	mode string,
) (publication PublicationRecord, err error) {
	err = s.db.WithinTenantUser(ctx, tenantID, userID, func(tx pgx.Tx) error {
		if err := requireCatalogManager(ctx, tx); err != nil {
			return err
		}
		// Versions are organization-wide. Lock the organization before reading
		// max(version) so concurrent publications serialize instead of racing a
		// uniqueness constraint after they have both modified catalog rows.
		var lockedTenantID string
		if err := tx.QueryRow(ctx, `
			SELECT id::text FROM tenants WHERE id = $1 FOR UPDATE`, tenantID,
		).Scan(&lockedTenantID); err != nil {
			return err
		}
		var locationID, status string
		if err := tx.QueryRow(ctx, `
			SELECT location_id::text, status FROM catalog_imports
			WHERE tenant_id = $1 AND id = $2 FOR UPDATE`, tenantID, importID).Scan(&locationID, &status); err != nil {
			return err
		}
		if status != "ready" {
			return ErrImportNotReady
		}
		plan, err := calculatePublishPlan(ctx, tx, tenantID, importID, mode)
		if err != nil {
			return err
		}
		if plan.Create+plan.Update == 0 {
			return ErrImportEmpty
		}

		affectedIDs, err := affectedExistingItemIDs(ctx, tx, tenantID, importID, locationID, mode)
		if err != nil {
			return err
		}
		before, err := loadCatalogItemsByID(ctx, tx, tenantID, affectedIDs)
		if err != nil {
			return err
		}

		if mode == "replace" {
			if _, err := tx.Exec(ctx, `
				UPDATE catalog_items item
				SET archived_at = now(), archived_by_user_id = $3, updated_by_user_id = $3
				WHERE item.tenant_id = $1 AND item.archived_at IS NULL
				  AND item.location_scope = 'selected'
				  AND (SELECT count(*) FROM catalog_item_locations il
				       WHERE il.tenant_id = item.tenant_id AND il.catalog_item_id = item.id) = 1
				  AND EXISTS (SELECT 1 FROM catalog_item_locations il
				      WHERE il.tenant_id = item.tenant_id AND il.catalog_item_id = item.id AND il.location_id = $2)
				  AND NOT EXISTS (SELECT 1 FROM catalog_import_rows row
				      WHERE row.tenant_id = $1 AND row.import_id = $4
				        AND row.classification = 'ambiguous' AND row.matching_item_id = item.id)`,
				tenantID, locationID, userID, importID); err != nil {
				return err
			}
		}

		rows, err := loadImportRows(ctx, tx, tenantID, importID)
		if err != nil {
			return err
		}
		for _, row := range rows {
			if row.Classification == "rejected" {
				continue
			}
			input := ItemInput{
				Kind: row.Kind, Reference: row.Reference, Name: row.Name, Description: row.Description,
				PriceKind: row.PriceKind, AmountCents: row.AmountCents, MaxAmountCents: row.MaxAmountCents,
				TaxBasis: row.TaxBasis, VATBasisPoints: row.VATBasisPoints,
				DurationMinutes: row.DurationMinutes, EffectiveFrom: row.EffectiveFrom,
				EffectiveTo: row.EffectiveTo, LocationScope: "selected", LocationIDs: []string{locationID},
			}
			if row.Classification == "ambiguous" {
				if _, err := updateImportedItem(ctx, tx, tenantID, userID, importID, row.MatchingItemID, input); err != nil {
					return mapCatalogWriteError(err)
				}
				continue
			}
			createdID, err := insertCatalogItem(ctx, tx, tenantID, userID, importID, input)
			if err != nil {
				return mapCatalogWriteError(err)
			}
			if err := replaceCatalogItemLocations(ctx, tx, tenantID, createdID, []string{locationID}); err != nil {
				return err
			}
			affectedIDs = append(affectedIDs, createdID)
		}
		affectedIDs = uniqueStrings(affectedIDs)
		after, err := loadCatalogItemsByID(ctx, tx, tenantID, affectedIDs)
		if err != nil {
			return err
		}
		beforeJSON, err := json.Marshal(before)
		if err != nil {
			return err
		}
		afterJSON, err := json.Marshal(after)
		if err != nil {
			return err
		}

		if mode != "merge" && mode != "replace" {
			return errors.New("invalid publication mode")
		}
		if err := tx.QueryRow(ctx, `
			INSERT INTO catalog_publications (
			    tenant_id, import_id, version, mode, published_by_user_id,
			    before_state, after_state
			) VALUES (
			    $1, $2,
			    COALESCE((SELECT max(version) + 1 FROM catalog_publications WHERE tenant_id = $1), 1),
			    $3, $4, $5, $6
			)
			RETURNING id::text, version, published_at`, tenantID, importID, mode, userID, beforeJSON, afterJSON,
		).Scan(&publication.ID, &publication.Version, &publication.PublishedAt); err != nil {
			return err
		}
		publication.ImportID, publication.Mode = importID, mode
		if _, err := tx.Exec(ctx, `
			UPDATE catalog_imports
			SET status = 'published', publication_mode = $3,
			    published_at = $4, published_by_user_id = $5
			WHERE tenant_id = $1 AND id = $2 AND status = 'ready'`,
			tenantID, importID, mode, publication.PublishedAt, userID); err != nil {
			return err
		}
		return nil
	})
	return publication, err
}

func (s *Store) Cancel(ctx context.Context, tenantID, userID, importID string) error {
	return s.db.WithinTenantUser(ctx, tenantID, userID, func(tx pgx.Tx) error {
		if err := requireCatalogManager(ctx, tx); err != nil {
			return err
		}
		result, err := tx.Exec(ctx, `
			UPDATE catalog_imports
			SET status = 'cancelled', cancelled_at = now(), cancelled_by_user_id = $3
			WHERE tenant_id = $1 AND id = $2 AND status = 'ready'`, tenantID, importID, userID)
		if err != nil {
			return err
		}
		if result.RowsAffected() != 1 {
			return ErrImportNotReady
		}
		return nil
	})
}

func (s *Store) Rollback(ctx context.Context, tenantID, userID, publicationID string) error {
	return s.db.WithinTenantUser(ctx, tenantID, userID, func(tx pgx.Tx) error {
		if err := requireCatalogManager(ctx, tx); err != nil {
			return err
		}
		var beforeJSON, afterJSON []byte
		var rolledBackAt sql.NullTime
		var version int
		if err := tx.QueryRow(ctx, `
			SELECT version, before_state, after_state, rolled_back_at
			FROM catalog_publications
			WHERE tenant_id = $1 AND id = $2 FOR UPDATE`, tenantID, publicationID,
		).Scan(&version, &beforeJSON, &afterJSON, &rolledBackAt); err != nil {
			return err
		}
		if rolledBackAt.Valid {
			return ErrAlreadyRolledBack
		}
		var later int
		if err := tx.QueryRow(ctx, `
			SELECT count(*) FROM catalog_publications
			WHERE tenant_id = $1 AND version > $2 AND rolled_back_at IS NULL`, tenantID, version).Scan(&later); err != nil {
			return err
		}
		if later != 0 {
			return ErrPublicationChanged
		}
		var before, after []CatalogItemRecord
		if err := json.Unmarshal(beforeJSON, &before); err != nil {
			return err
		}
		if err := json.Unmarshal(afterJSON, &after); err != nil {
			return err
		}
		ids := make([]string, 0, len(after))
		for _, item := range after {
			ids = append(ids, item.ID)
		}
		current, err := loadCatalogItemsByID(ctx, tx, tenantID, ids)
		if err != nil {
			return err
		}
		currentJSON, err := json.Marshal(current)
		if err != nil {
			return err
		}
		canonicalAfter, err := json.Marshal(after)
		if err != nil {
			return err
		}
		if !stringSlicesEqual(currentJSON, canonicalAfter) {
			return ErrPublicationChanged
		}
		beforeByID := make(map[string]CatalogItemRecord, len(before))
		for _, item := range before {
			beforeByID[item.ID] = item
		}
		for _, item := range after {
			previous, existed := beforeByID[item.ID]
			if !existed {
				if _, err := tx.Exec(ctx, `
					UPDATE catalog_items SET archived_at = now(), archived_by_user_id = $2, updated_by_user_id = $2
					WHERE tenant_id = $1 AND id = $3`, tenantID, userID, item.ID); err != nil {
					return err
				}
				continue
			}
			if err := restoreCatalogItem(ctx, tx, tenantID, userID, previous); err != nil {
				return mapCatalogWriteError(err)
			}
		}
		result, err := tx.Exec(ctx, `
			UPDATE catalog_publications
			SET rolled_back_at = now(), rolled_back_by_user_id = $3
			WHERE tenant_id = $1 AND id = $2 AND rolled_back_at IS NULL`, tenantID, publicationID, userID)
		if err != nil {
			return err
		}
		if result.RowsAffected() != 1 {
			return ErrAlreadyRolledBack
		}
		return nil
	})
}

const catalogItemSelect = `
	SELECT item.id::text, item.kind, COALESCE(item.reference, ''), item.name,
	       COALESCE(item.description, ''), item.price_kind,
	       item.amount_cents, item.max_amount_cents, item.tax_basis,
	       item.vat_basis_points, item.currency, item.per_hour,
	       item.duration_minutes, item.effective_from, item.effective_to,
	       item.location_scope, COALESCE(item.source_import_id::text, ''),
	       item.created_at, item.updated_at, item.archived_at,
	       COALESCE((SELECT jsonb_agg(il.location_id::text ORDER BY il.location_id)
	                 FROM catalog_item_locations il
	                 WHERE il.tenant_id = item.tenant_id AND il.catalog_item_id = item.id), '[]'::jsonb),
	       COALESCE((SELECT jsonb_agg(location.name ORDER BY location.name, location.id)
	                 FROM catalog_item_locations il
	                 JOIN locations location ON location.tenant_id = il.tenant_id AND location.id = il.location_id
	                 WHERE il.tenant_id = item.tenant_id AND il.catalog_item_id = item.id), '[]'::jsonb)
	FROM catalog_items item`

const importSelect = `
	SELECT catalog_import.id::text, catalog_import.location_id::text, location.name,
	       catalog_import.source_filename, catalog_import.source_format,
	       catalog_import.source_media_type, catalog_import.source_size_bytes,
	       catalog_import.source_sha256, COALESCE(uploader.name, uploader.email),
	       catalog_import.created_at, catalog_import.status,
	       COALESCE(catalog_import.rejection_reason, ''),
	       COALESCE(catalog_import.publication_mode, ''), catalog_import.published_at,
	       count(*) FILTER (WHERE row.classification = 'valid'),
	       count(*) FILTER (WHERE row.classification = 'ambiguous'),
	       count(*) FILTER (WHERE row.classification = 'rejected')
	FROM catalog_imports catalog_import
	JOIN locations location ON location.tenant_id = catalog_import.tenant_id AND location.id = catalog_import.location_id
	JOIN users uploader ON uploader.id = catalog_import.uploaded_by_user_id
	LEFT JOIN catalog_import_rows row ON row.tenant_id = catalog_import.tenant_id AND row.import_id = catalog_import.id`

func scanCatalogItems(rows pgx.Rows) (items []CatalogItemRecord, err error) {
	defer rows.Close()
	for rows.Next() {
		var item CatalogItemRecord
		var amount, maximum sql.NullInt64
		var duration sql.NullInt64
		var from, to sql.NullTime
		var archived sql.NullTime
		var locationIDs, locationNames []byte
		if err := rows.Scan(
			&item.ID, &item.Kind, &item.Reference, &item.Name, &item.Description,
			&item.PriceKind, &amount, &maximum, &item.TaxBasis, &item.VATBasisPoints,
			&item.Currency, &item.PerHour, &duration, &from, &to, &item.LocationScope,
			&item.SourceImportID, &item.CreatedAt, &item.UpdatedAt, &archived,
			&locationIDs, &locationNames,
		); err != nil {
			return nil, err
		}
		item.AmountCents = nullInt64Pointer(amount)
		item.MaxAmountCents = nullInt64Pointer(maximum)
		item.DurationMinutes = nullIntPointer(duration)
		item.EffectiveFrom = nullTimePointer(from)
		item.EffectiveTo = nullTimePointer(to)
		item.ArchivedAt = nullTimePointer(archived)
		if err := json.Unmarshal(locationIDs, &item.LocationIDs); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(locationNames, &item.LocationNames); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func loadCatalogItemsByID(ctx context.Context, tx pgx.Tx, tenantID string, ids []string) ([]CatalogItemRecord, error) {
	ids = uniqueStrings(ids)
	if len(ids) == 0 {
		return []CatalogItemRecord{}, nil
	}
	rows, err := tx.Query(ctx, catalogItemSelect+`
		WHERE item.tenant_id = $1 AND item.id = ANY($2::uuid[])
		ORDER BY item.id`, tenantID, ids)
	if err != nil {
		return nil, err
	}
	return scanCatalogItems(rows)
}

func insertCatalogItem(ctx context.Context, tx pgx.Tx, tenantID, userID, importID string, input ItemInput) (string, error) {
	var source any
	if importID != "" {
		source = importID
	}
	var id string
	err := tx.QueryRow(ctx, `
		INSERT INTO catalog_items (
		    tenant_id, kind, reference, name, description, price_kind,
		    amount_cents, max_amount_cents, tax_basis, vat_basis_points,
		    per_hour, duration_minutes, effective_from, effective_to,
		    location_scope, source_import_id, created_by_user_id, updated_by_user_id
		) VALUES (
		    $1, $2, NULLIF($3, ''), $4, NULLIF($5, ''), $6,
		    $7, $8, $9, $10, ($2 = 'labour_rate'), $11, $12, $13,
		    $14, $15, $16, $16
		) RETURNING id::text`, tenantID, input.Kind, strings.TrimSpace(input.Reference),
		strings.TrimSpace(input.Name), strings.TrimSpace(input.Description), input.PriceKind,
		input.AmountCents, input.MaxAmountCents, input.TaxBasis, input.VATBasisPoints,
		input.DurationMinutes, input.EffectiveFrom, input.EffectiveTo, input.LocationScope,
		source, userID).Scan(&id)
	return id, err
}

func updateImportedItem(ctx context.Context, tx pgx.Tx, tenantID, userID, importID, itemID string, input ItemInput) (string, error) {
	result, err := tx.Exec(ctx, `
		UPDATE catalog_items SET
		    kind = $4, reference = NULLIF($5, ''), name = $6,
		    description = NULLIF($7, ''), price_kind = $8,
		    amount_cents = $9, max_amount_cents = $10,
		    tax_basis = $11, vat_basis_points = $12,
		    per_hour = ($4 = 'labour_rate'), duration_minutes = $13,
		    effective_from = $14, effective_to = $15,
		    source_import_id = $3, updated_by_user_id = $2
		WHERE tenant_id = $1 AND id = $16 AND archived_at IS NULL`,
		tenantID, userID, importID, input.Kind, strings.TrimSpace(input.Reference), input.Name,
		input.Description, input.PriceKind, input.AmountCents, input.MaxAmountCents,
		input.TaxBasis, input.VATBasisPoints, input.DurationMinutes, input.EffectiveFrom,
		input.EffectiveTo, itemID)
	if err != nil {
		return "", err
	}
	if result.RowsAffected() != 1 {
		return "", ErrPublicationChanged
	}
	return itemID, nil
}

func replaceCatalogItemLocations(ctx context.Context, tx pgx.Tx, tenantID, itemID string, locationIDs []string) error {
	if _, err := tx.Exec(ctx, `DELETE FROM catalog_item_locations WHERE tenant_id = $1 AND catalog_item_id = $2`, tenantID, itemID); err != nil {
		return err
	}
	for _, locationID := range locationIDs {
		if _, err := tx.Exec(ctx, `
			INSERT INTO catalog_item_locations (tenant_id, catalog_item_id, location_id)
			VALUES ($1, $2, $3)`, tenantID, itemID, locationID); err != nil {
			return err
		}
	}
	return nil
}

func validateCatalogLocations(ctx context.Context, tx pgx.Tx, tenantID, scope string, requested []string) ([]string, error) {
	requested = uniqueStrings(requested)
	if scope == "all" {
		if len(requested) != 0 {
			return nil, errors.New("all-location scope cannot include locations")
		}
		return nil, nil
	}
	if scope != "selected" || len(requested) == 0 {
		return nil, ErrInvalidLocation
	}
	var count int
	if err := tx.QueryRow(ctx, `
		SELECT count(*) FROM locations
		WHERE tenant_id = $1 AND id = ANY($2::uuid[]) AND status = 'active'`, tenantID, requested).Scan(&count); err != nil {
		return nil, err
	}
	if count != len(requested) {
		return nil, ErrInvalidLocation
	}
	return requested, nil
}

func loadCatalogPrincipal(ctx context.Context, tx pgx.Tx, tenantID, userID string, organization *string) (string, error) {
	var role, name string
	if err := tx.QueryRow(ctx, `
		SELECT membership.role, tenant.name
		FROM tenant_memberships membership
		JOIN tenants tenant ON tenant.id = membership.tenant_id
		WHERE membership.tenant_id = $1 AND membership.user_id = $2`, tenantID, userID).Scan(&role, &name); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", ErrForbidden
		}
		return "", err
	}
	if organization != nil {
		*organization = name
	}
	return role, nil
}

func requireCatalogManager(ctx context.Context, tx pgx.Tx) error {
	var allowed bool
	if err := tx.QueryRow(ctx, `SELECT app_current_user_manages_tenant()`).Scan(&allowed); err != nil {
		return err
	}
	if !allowed {
		return ErrForbidden
	}
	return nil
}

func loadCatalogLocations(ctx context.Context, tx pgx.Tx, tenantID string) ([]CatalogLocation, error) {
	rows, err := tx.Query(ctx, `
		SELECT id::text, name, status = 'active'
		FROM locations WHERE tenant_id = $1
		ORDER BY status <> 'active', name, id`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var locations []CatalogLocation
	for rows.Next() {
		var location CatalogLocation
		if err := rows.Scan(&location.ID, &location.Name, &location.Active); err != nil {
			return nil, err
		}
		locations = append(locations, location)
	}
	return locations, rows.Err()
}

func classifyAndInsertRows(ctx context.Context, tx pgx.Tx, tenantID, locationID, importID string, parsed []parsedRow) error {
	seen := make(map[string]struct{})
	for _, row := range parsed {
		classification, issue := "valid", row.Issue
		var matchingID any
		if issue != "" {
			classification = "rejected"
		} else {
			key := strings.ToLower(strings.TrimSpace(row.Values.Reference))
			if key == "" {
				key = "name:" + fold(row.Values.Name)
			}
			if _, duplicate := seen[key]; duplicate {
				classification, issue = "rejected", "Ligne en double dans ce fichier"
			} else {
				seen[key] = struct{}{}
				id, name, safe, err := findImportCollision(ctx, tx, tenantID, locationID, row.Values)
				if err != nil {
					return err
				}
				if id != "" && safe {
					classification, issue, matchingID = "ambiguous", "Mettra à jour « "+name+" »", id
				} else if id != "" {
					classification, issue = "rejected", "Correspond à une offre partagée avec d’autres sites"
				}
			}
		}
		rawJSON, err := json.Marshal(row.Raw)
		if err != nil {
			return err
		}
		values := row.Values
		if _, err := tx.Exec(ctx, `
			INSERT INTO catalog_import_rows (
			    tenant_id, import_id, row_number, classification, raw_data,
			    kind, reference, name, description, price_kind,
			    amount_cents, max_amount_cents, tax_basis, vat_basis_points,
			    duration_minutes, effective_from, effective_to, issue, matching_item_id
			) VALUES (
			    $1, $2, $3, $4, $5,
			    NULLIF($6, ''), NULLIF($7, ''), NULLIF($8, ''), NULLIF($9, ''), NULLIF($10, ''),
			    $11, $12, NULLIF($13, ''), $14, $15, $16, $17, NULLIF($18, ''), $19
			)`, tenantID, importID, row.Number, classification, rawJSON,
			values.Kind, values.Reference, values.Name, values.Description, values.PriceKind,
			values.AmountCents, values.MaxAmountCents, values.TaxBasis, values.VATBasisPoints,
			values.DurationMinutes, values.EffectiveFrom, values.EffectiveTo, issue, matchingID); err != nil {
			return err
		}
	}
	return nil
}

func findImportCollision(ctx context.Context, tx pgx.Tx, tenantID, locationID string, input ItemInput) (id, name string, safe bool, err error) {
	err = tx.QueryRow(ctx, `
		SELECT item.id::text, item.name,
		       item.location_scope = 'selected'
		       AND (SELECT count(*) FROM catalog_item_locations il
		            WHERE il.tenant_id = item.tenant_id AND il.catalog_item_id = item.id) = 1
		       AND EXISTS (SELECT 1 FROM catalog_item_locations il
		            WHERE il.tenant_id = item.tenant_id AND il.catalog_item_id = item.id AND il.location_id = $2)
		FROM catalog_items item
		WHERE item.tenant_id = $1 AND item.archived_at IS NULL
		  AND (
		      (NULLIF($3, '') IS NOT NULL AND lower(item.reference) = lower($3))
		      OR (NULLIF($3, '') IS NULL AND lower(regexp_replace(btrim(item.name), '\\s+', ' ', 'g')) = lower($4))
		  )
		ORDER BY item.reference IS NULL, item.id
		LIMIT 1`, tenantID, locationID, strings.TrimSpace(input.Reference), fold(input.Name)).Scan(&id, &name, &safe)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", false, nil
	}
	return
}

func loadImport(ctx context.Context, tx pgx.Tx, tenantID, importID string) (ImportRecord, error) {
	rows, err := tx.Query(ctx, importSelect+`
		WHERE catalog_import.tenant_id = $1 AND catalog_import.id = $2
		GROUP BY catalog_import.id, location.name, uploader.name, uploader.email`, tenantID, importID)
	if err != nil {
		return ImportRecord{}, err
	}
	records, err := scanImports(rows)
	if err != nil {
		return ImportRecord{}, err
	}
	if len(records) == 0 {
		return ImportRecord{}, sql.ErrNoRows
	}
	return records[0], nil
}

func scanImports(rows pgx.Rows) (records []ImportRecord, err error) {
	defer rows.Close()
	for rows.Next() {
		var record ImportRecord
		var published sql.NullTime
		if err := rows.Scan(
			&record.ID, &record.LocationID, &record.LocationName, &record.Filename,
			&record.Format, &record.MediaType, &record.Size, &record.Checksum,
			&record.UploadedBy, &record.UploadedAt, &record.Status, &record.Rejection,
			&record.Mode, &published, &record.ValidRows, &record.AmbiguousRows, &record.RejectedRows,
		); err != nil {
			return nil, err
		}
		record.PublishedAt = nullTimePointer(published)
		records = append(records, record)
	}
	return records, rows.Err()
}

func loadImportRows(ctx context.Context, tx pgx.Tx, tenantID, importID string) (records []ImportRowRecord, err error) {
	rows, err := tx.Query(ctx, `
		SELECT row.row_number, row.classification, row.raw_data,
		       COALESCE(row.kind, ''), COALESCE(row.reference, ''), COALESCE(row.name, ''),
		       COALESCE(row.description, ''), COALESCE(row.price_kind, ''),
		       row.amount_cents, row.max_amount_cents, COALESCE(row.tax_basis, ''),
		       COALESCE(row.vat_basis_points, 0), row.duration_minutes,
		       row.effective_from, row.effective_to, COALESCE(row.issue, ''),
		       COALESCE(row.matching_item_id::text, ''), COALESCE(item.name, '')
		FROM catalog_import_rows row
		LEFT JOIN catalog_items item ON item.tenant_id = row.tenant_id AND item.id = row.matching_item_id
		WHERE row.tenant_id = $1 AND row.import_id = $2
		ORDER BY row.row_number`, tenantID, importID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var record ImportRowRecord
		var raw []byte
		var amount, maximum, duration sql.NullInt64
		var from, to sql.NullTime
		if err := rows.Scan(
			&record.Number, &record.Classification, &raw, &record.Kind, &record.Reference,
			&record.Name, &record.Description, &record.PriceKind, &amount, &maximum,
			&record.TaxBasis, &record.VATBasisPoints, &duration, &from, &to, &record.Issue,
			&record.MatchingItemID, &record.MatchingName,
		); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(raw, &record.Raw); err != nil {
			return nil, err
		}
		record.AmountCents, record.MaxAmountCents = nullInt64Pointer(amount), nullInt64Pointer(maximum)
		record.DurationMinutes = nullIntPointer(duration)
		record.EffectiveFrom, record.EffectiveTo = nullTimePointer(from), nullTimePointer(to)
		records = append(records, record)
	}
	return records, rows.Err()
}

func calculatePublishPlan(ctx context.Context, tx pgx.Tx, tenantID, importID, mode string) (PublishPlan, error) {
	if mode != "merge" && mode != "replace" {
		return PublishPlan{}, errors.New("invalid publication mode")
	}
	var plan PublishPlan
	var locationID, status string
	if err := tx.QueryRow(ctx, `SELECT location_id::text, status FROM catalog_imports WHERE tenant_id = $1 AND id = $2`, tenantID, importID).Scan(&locationID, &status); err != nil {
		return plan, err
	}
	if status != "ready" {
		return plan, ErrImportNotReady
	}
	if err := tx.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE classification = 'valid'),
		       count(*) FILTER (WHERE classification = 'ambiguous'),
		       count(*) FILTER (WHERE classification = 'rejected')
		FROM catalog_import_rows WHERE tenant_id = $1 AND import_id = $2`, tenantID, importID,
	).Scan(&plan.Create, &plan.Update, &plan.Skip); err != nil {
		return plan, err
	}
	if mode == "replace" {
		if err := tx.QueryRow(ctx, `
			SELECT count(*) FROM catalog_items item
			WHERE item.tenant_id = $1 AND item.archived_at IS NULL
			  AND item.location_scope = 'selected'
			  AND (SELECT count(*) FROM catalog_item_locations il
			       WHERE il.tenant_id = item.tenant_id AND il.catalog_item_id = item.id) = 1
			  AND EXISTS (SELECT 1 FROM catalog_item_locations il
			      WHERE il.tenant_id = item.tenant_id AND il.catalog_item_id = item.id AND il.location_id = $2)
			  AND NOT EXISTS (SELECT 1 FROM catalog_import_rows row
			      WHERE row.tenant_id = $1 AND row.import_id = $3
			        AND row.classification = 'ambiguous' AND row.matching_item_id = item.id)`, tenantID, locationID, importID).Scan(&plan.Remove); err != nil {
			return plan, err
		}
	}
	return plan, nil
}

func affectedExistingItemIDs(ctx context.Context, tx pgx.Tx, tenantID, importID, locationID, mode string) ([]string, error) {
	query := `
		SELECT DISTINCT matching_item_id::text FROM catalog_import_rows
		WHERE tenant_id = $1 AND import_id = $2 AND classification = 'ambiguous'`
	args := []any{tenantID, importID}
	if mode == "replace" {
		query += ` UNION SELECT item.id::text FROM catalog_items item
			WHERE item.tenant_id = $1 AND item.archived_at IS NULL AND item.location_scope = 'selected'
			  AND (SELECT count(*) FROM catalog_item_locations il WHERE il.tenant_id = item.tenant_id AND il.catalog_item_id = item.id) = 1
			  AND EXISTS (SELECT 1 FROM catalog_item_locations il WHERE il.tenant_id = item.tenant_id AND il.catalog_item_id = item.id AND il.location_id = $3)`
		args = append(args, locationID)
	}
	rows, err := tx.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return uniqueStrings(ids), rows.Err()
}

func restoreCatalogItem(ctx context.Context, tx pgx.Tx, tenantID, userID string, item CatalogItemRecord) error {
	var archivedBy any
	if item.ArchivedAt != nil {
		archivedBy = userID
	}
	if _, err := tx.Exec(ctx, `
		UPDATE catalog_items SET
		    kind = $3, reference = NULLIF($4, ''), name = $5, description = NULLIF($6, ''),
		    price_kind = $7, amount_cents = $8, max_amount_cents = $9,
		    tax_basis = $10, vat_basis_points = $11, currency = $12, per_hour = $13,
		    duration_minutes = $14, effective_from = $15, effective_to = $16,
		    location_scope = $17, source_import_id = NULLIF($18, '')::uuid,
		    updated_by_user_id = $2, archived_at = $19, archived_by_user_id = $20
		WHERE tenant_id = $1 AND id = $21`, tenantID, userID, item.Kind, item.Reference,
		item.Name, item.Description, item.PriceKind, item.AmountCents, item.MaxAmountCents,
		item.TaxBasis, item.VATBasisPoints, item.Currency, item.PerHour, item.DurationMinutes,
		item.EffectiveFrom, item.EffectiveTo, item.LocationScope, item.SourceImportID,
		item.ArchivedAt, archivedBy, item.ID); err != nil {
		return err
	}
	return replaceCatalogItemLocations(ctx, tx, tenantID, item.ID, item.LocationIDs)
}

func mapCatalogWriteError(err error) error {
	var pgError *pgconn.PgError
	if errors.As(err, &pgError) {
		if pgError.Code == "23505" && pgError.ConstraintName == "catalog_items_active_reference_unique" {
			return ErrDuplicateReference
		}
		if pgError.Code == "23505" {
			return ErrDuplicateReference
		}
		if pgError.Code == "23514" || pgError.Code == "23503" {
			return fmt.Errorf("catalog data violates database validation: %w", err)
		}
	}
	return err
}

func nullInt64Pointer(value sql.NullInt64) *int64 {
	if !value.Valid {
		return nil
	}
	result := value.Int64
	return &result
}
func nullIntPointer(value sql.NullInt64) *int {
	if !value.Valid {
		return nil
	}
	result := int(value.Int64)
	return &result
}
func nullTimePointer(value sql.NullTime) *time.Time {
	if !value.Valid {
		return nil
	}
	result := value.Time
	return &result
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func stringSlicesEqual(left, right []byte) bool { return string(left) == string(right) }
