package accesscontrol

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/esrid/garageband/internal/platform/db"
)

var ErrForbidden = errors.New("access control operation is forbidden")

type LocationAssignment struct {
	ID         string       `db:"id"`
	TenantID   string       `db:"tenant_id"`
	UserID     string       `db:"user_id"`
	LocationID string       `db:"location_id"`
	AssignedBy string       `db:"assigned_by"`
	AssignedAt time.Time    `db:"assigned_at"`
	RevokedBy  string       `db:"revoked_by"`
	RevokedAt  sql.NullTime `db:"revoked_at"`
}

type CustomerGrant struct {
	ID                  string       `db:"id"`
	TenantID            string       `db:"tenant_id"`
	CustomerID          string       `db:"customer_id"`
	SourceLocationID    string       `db:"source_location_id"`
	ReceivingLocationID string       `db:"receiving_location_id"`
	GrantedBy           string       `db:"granted_by"`
	GrantedAt           time.Time    `db:"granted_at"`
	RevokedBy           string       `db:"revoked_by"`
	RevokedAt           sql.NullTime `db:"revoked_at"`
}

type TeamLocation struct {
	ID     string `db:"id"`
	Name   string `db:"name"`
	Active bool   `db:"active"`
}

type TeamMember struct {
	UserID      string
	Name        string
	Email       string
	Role        string
	LocationIDs []string
}

type TeamOverview struct {
	Organization string
	ActorRole    string
	CanManage    bool
	Locations    []TeamLocation
	Members      []TeamMember
}

type Store struct{ db *db.DB }

func NewStore(database *db.DB) *Store { return &Store{db: database} }

func (s *Store) TeamOverview(
	ctx context.Context,
	tenantID string,
	actorUserID string,
) (overview TeamOverview, err error) {
	err = s.db.WithinTenantUser(ctx, tenantID, actorUserID, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `
			SELECT tenant.name, membership.role
			FROM tenants tenant
			JOIN tenant_memberships membership
			  ON membership.tenant_id = tenant.id
			WHERE tenant.id = $1 AND membership.user_id = $2`,
			tenantID, actorUserID,
		).Scan(&overview.Organization, &overview.ActorRole); err != nil {
			return err
		}
		overview.CanManage = overview.ActorRole == "owner" || overview.ActorRole == "admin"

		locationRows, err := tx.Query(ctx, `
			SELECT id, name, status = 'active' AS active
			FROM locations
			WHERE tenant_id = $1
			ORDER BY (status = 'active') DESC, name, id`, tenantID)
		if err != nil {
			return err
		}
		overview.Locations, err = pgx.CollectRows(locationRows, pgx.RowToStructByName[TeamLocation])
		if err != nil {
			return err
		}

		memberRows, err := tx.Query(ctx, `
			SELECT membership.user_id, user_account.name, user_account.email,
			       membership.role,
			       COALESCE(jsonb_agg(assignment.location_id::text)
			           FILTER (WHERE assignment.id IS NOT NULL), '[]'::JSONB)
			FROM tenant_memberships membership
			JOIN users user_account ON user_account.id = membership.user_id
			LEFT JOIN user_location_assignments assignment
			  ON assignment.tenant_id = membership.tenant_id
			 AND assignment.user_id = membership.user_id
			 AND assignment.revoked_at IS NULL
			WHERE membership.tenant_id = $1
			  AND ($2 OR membership.user_id = $3)
			GROUP BY membership.user_id, user_account.name, user_account.email,
			         membership.role, membership.created_at
			ORDER BY membership.created_at, membership.user_id`,
			tenantID, overview.CanManage, actorUserID)
		if err != nil {
			return err
		}
		defer memberRows.Close()
		for memberRows.Next() {
			var member TeamMember
			var locationIDsJSON []byte
			if err := memberRows.Scan(
				&member.UserID, &member.Name, &member.Email,
				&member.Role, &locationIDsJSON,
			); err != nil {
				return err
			}
			if err := json.Unmarshal(locationIDsJSON, &member.LocationIDs); err != nil {
				return err
			}
			slices.Sort(member.LocationIDs)
			overview.Members = append(overview.Members, member)
		}
		return memberRows.Err()
	})
	return overview, err
}

func (s *Store) ReplaceLocationAssignments(
	ctx context.Context,
	tenantID string,
	actorUserID string,
	targetUserID string,
	desiredLocationIDs []string,
) error {
	return s.db.WithinTenantUser(ctx, tenantID, actorUserID, func(tx pgx.Tx) error {
		if err := requireAdministrator(ctx, tx, tenantID, actorUserID); err != nil {
			return err
		}
		var targetRole string
		if err := tx.QueryRow(ctx, `
			SELECT role FROM tenant_memberships
			WHERE tenant_id = $1 AND user_id = $2`, tenantID, targetUserID,
		).Scan(&targetRole); err != nil {
			return err
		}
		if targetRole == "owner" || targetRole == "admin" {
			return ErrForbidden
		}

		desired := make(map[string]struct{}, len(desiredLocationIDs))
		for _, locationID := range desiredLocationIDs {
			if _, duplicate := desired[locationID]; duplicate {
				continue
			}
			var exists bool
			if err := tx.QueryRow(ctx, `
				SELECT EXISTS (
					SELECT 1 FROM locations
					WHERE tenant_id = $1 AND id = $2
				)`, tenantID, locationID,
			).Scan(&exists); err != nil {
				return err
			}
			if !exists {
				return sql.ErrNoRows
			}
			desired[locationID] = struct{}{}
		}

		rows, err := tx.Query(ctx, `
			SELECT id, location_id
			FROM user_location_assignments
			WHERE tenant_id = $1 AND user_id = $2 AND revoked_at IS NULL`,
			tenantID, targetUserID)
		if err != nil {
			return err
		}
		current := make(map[string]string)
		for rows.Next() {
			var assignmentID, locationID string
			if err := rows.Scan(&assignmentID, &locationID); err != nil {
				rows.Close()
				return err
			}
			current[locationID] = assignmentID
		}
		rowsErr := rows.Err()
		rows.Close()
		if rowsErr != nil {
			return rowsErr
		}

		for locationID, assignmentID := range current {
			if _, keep := desired[locationID]; keep {
				continue
			}
			var revokedID string
			if err := tx.QueryRow(ctx, `
				UPDATE user_location_assignments
				SET revoked_by_user_id = $3, revoked_at = now()
				WHERE tenant_id = $1 AND id = $2 AND revoked_at IS NULL
				RETURNING id`,
				tenantID, assignmentID, actorUserID,
			).Scan(&revokedID); err != nil {
				return err
			}
		}
		for locationID := range desired {
			if _, alreadyAssigned := current[locationID]; alreadyAssigned {
				continue
			}
			var assignmentID string
			if err := tx.QueryRow(ctx, `
				INSERT INTO user_location_assignments (
					tenant_id, user_id, location_id, assigned_by_user_id
				) VALUES ($1, $2, $3, $4)
				RETURNING id`,
				tenantID, targetUserID, locationID, actorUserID,
			).Scan(&assignmentID); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *Store) AssignLocation(
	ctx context.Context,
	tenantID string,
	actorUserID string,
	targetUserID string,
	locationID string,
) (assignment LocationAssignment, err error) {
	err = s.db.WithinTenantUser(ctx, tenantID, actorUserID, func(tx pgx.Tx) error {
		if err := requireAdministrator(ctx, tx, tenantID, actorUserID); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `
			SELECT id, tenant_id, user_id, location_id,
			       assigned_by_user_id AS assigned_by,
			       assigned_at, COALESCE(revoked_by_user_id::text, '') AS revoked_by,
			       revoked_at
			FROM user_location_assignments
			WHERE tenant_id = $1 AND user_id = $2 AND location_id = $3
			  AND revoked_at IS NULL`, tenantID, targetUserID, locationID)
		if err != nil {
			return err
		}
		if a, scanErr := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[LocationAssignment]); scanErr == nil {
			assignment = a
			return nil
		} else if !errors.Is(scanErr, pgx.ErrNoRows) {
			return scanErr
		}
		rows, err = tx.Query(ctx, `
			INSERT INTO user_location_assignments (
				tenant_id, user_id, location_id, assigned_by_user_id
			) VALUES ($1, $2, $3, $4)
			RETURNING id, tenant_id, user_id, location_id,
			          assigned_by_user_id AS assigned_by,
			          assigned_at, COALESCE(revoked_by_user_id::text, '') AS revoked_by,
			          revoked_at`,
			tenantID, targetUserID, locationID, actorUserID,
		)
		if err != nil {
			return err
		}
		assignment, err = pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[LocationAssignment])
		return err
	})
	return assignment, err
}

func (s *Store) RevokeLocationAssignment(
	ctx context.Context,
	tenantID string,
	actorUserID string,
	assignmentID string,
) (assignment LocationAssignment, err error) {
	err = s.db.WithinTenantUser(ctx, tenantID, actorUserID, func(tx pgx.Tx) error {
		if err := requireAdministrator(ctx, tx, tenantID, actorUserID); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `
			UPDATE user_location_assignments
			SET revoked_by_user_id = $3, revoked_at = now()
			WHERE tenant_id = $1 AND id = $2 AND revoked_at IS NULL
			RETURNING id, tenant_id, user_id, location_id,
			          assigned_by_user_id AS assigned_by,
			          assigned_at, COALESCE(revoked_by_user_id::text, '') AS revoked_by,
			          revoked_at`,
			tenantID, assignmentID, actorUserID,
		)
		if err != nil {
			return err
		}
		assignment, err = pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[LocationAssignment])
		return err
	})
	return assignment, err
}

func (s *Store) GrantCustomer(
	ctx context.Context,
	tenantID string,
	actorUserID string,
	customerID string,
	receivingLocationID string,
) (grant CustomerGrant, err error) {
	err = s.db.WithinTenantUser(ctx, tenantID, actorUserID, func(tx pgx.Tx) error {
		if err := requireAdministrator(ctx, tx, tenantID, actorUserID); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `
			SELECT id, tenant_id, customer_id, source_location_id,
			       receiving_location_id, granted_by_user_id AS granted_by,
			       granted_at,
			       COALESCE(revoked_by_user_id::text, '') AS revoked_by, revoked_at
			FROM customer_location_grants
			WHERE tenant_id = $1 AND customer_id = $2
			  AND receiving_location_id = $3 AND revoked_at IS NULL`,
			tenantID, customerID, receivingLocationID)
		if err != nil {
			return err
		}
		if g, scanErr := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[CustomerGrant]); scanErr == nil {
			grant = g
			return nil
		} else if !errors.Is(scanErr, pgx.ErrNoRows) {
			return scanErr
		}
		rows, err = tx.Query(ctx, `
			INSERT INTO customer_location_grants (
				tenant_id, customer_id, source_location_id,
				receiving_location_id, granted_by_user_id
			)
			SELECT customer.tenant_id, customer.id, customer.home_location_id,
			       $3, $4
			FROM customers customer
			WHERE customer.tenant_id = $1 AND customer.id = $2
			RETURNING id, tenant_id, customer_id, source_location_id,
			          receiving_location_id, granted_by_user_id AS granted_by,
			          granted_at,
			          COALESCE(revoked_by_user_id::text, '') AS revoked_by, revoked_at`,
			tenantID, customerID, receivingLocationID, actorUserID,
		)
		if err != nil {
			return err
		}
		grant, err = pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[CustomerGrant])
		return err
	})
	return grant, err
}

func (s *Store) RevokeCustomerGrant(
	ctx context.Context,
	tenantID string,
	actorUserID string,
	grantID string,
) (grant CustomerGrant, err error) {
	err = s.db.WithinTenantUser(ctx, tenantID, actorUserID, func(tx pgx.Tx) error {
		if err := requireAdministrator(ctx, tx, tenantID, actorUserID); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `
			UPDATE customer_location_grants
			SET revoked_by_user_id = $3, revoked_at = now()
			WHERE tenant_id = $1 AND id = $2 AND revoked_at IS NULL
			RETURNING id, tenant_id, customer_id, source_location_id,
			          receiving_location_id, granted_by_user_id AS granted_by,
			          granted_at,
			          COALESCE(revoked_by_user_id::text, '') AS revoked_by, revoked_at`,
			tenantID, grantID, actorUserID,
		)
		if err != nil {
			return err
		}
		grant, err = pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[CustomerGrant])
		return err
	})
	return grant, err
}

func requireAdministrator(
	ctx context.Context,
	tx pgx.Tx,
	tenantID string,
	userID string,
) error {
	var allowed bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM tenant_memberships
			WHERE tenant_id = $1 AND user_id = $2
			  AND role IN ('owner', 'admin')
		)`, tenantID, userID,
	).Scan(&allowed); err != nil {
		return err
	}
	if !allowed {
		return ErrForbidden
	}
	return nil
}
