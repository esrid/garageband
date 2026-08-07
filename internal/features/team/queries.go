package team

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/esrid/garageband/internal/platform/db"
)

var (
	ErrForbidden    = errors.New("team access modification is forbidden")
	ErrNameRequired = errors.New("staff member name is required")
)

// inviteTTL bounds how long a link handed out on a phone screen stays usable.
// ponytail: re-inviting from the team screen mints a new one.
const inviteTTL = 7 * 24 * time.Hour

// inviteCodeLength is a compromise between a secret and something a garage
// owner can read out loud across a workshop. rand.Text() emits RFC 4648 base32,
// whose alphabet (A-Z, 2-7) has no 0/O or 1/I to mishear, so 12 characters
// carry 60 bits: at a thousand guesses a second for the whole week the code
// lives, the odds of hitting one are about one in two billion.
const inviteCodeLength = 12

// StaffInvite is a freshly minted invitation. Token holds the only copy of the
// raw value that will ever exist: the database keeps its hash, so the code can
// be shown once and never recovered.
type StaffInvite struct {
	UserID string
	Token  string
}

type Store struct{ db *db.DB }

func NewStore(database *db.DB) *Store { return &Store{db: database} }

// Page reads the whole screen in one transaction: the organization, its sites,
// and who reaches which. A member who cannot manage sees only themselves.
func (s *Store) Page(
	ctx context.Context,
	tenantID string,
	actorUserID string,
) (page Page, err error) {
	err = s.db.WithinTenantUser(ctx, tenantID, actorUserID, func(tx pgx.Tx) error {
		var actorRole string
		if err := tx.QueryRow(ctx, `
			SELECT tenant.name, membership.role
			FROM tenants tenant
			JOIN tenant_memberships membership
			  ON membership.tenant_id = tenant.id
			WHERE tenant.id = $1 AND membership.user_id = $2`,
			tenantID, actorUserID,
		).Scan(&page.Organization, &actorRole); err != nil {
			return err
		}
		page.CanManage = actorRole == RoleOwner || actorRole == RoleAdmin

		locationRows, err := tx.Query(ctx, `
			SELECT id, name, status = 'active' AS active
			FROM locations
			WHERE tenant_id = $1
			ORDER BY (status = 'active') DESC, name, id`, tenantID)
		if err != nil {
			return err
		}
		page.Locations, err = pgx.CollectRows(locationRows, pgx.RowToStructByName[LocationRef])
		if err != nil {
			return err
		}

		memberRows, err := tx.Query(ctx, `
			SELECT membership.user_id, user_account.name, user_account.email,
			       membership.role,
			       COALESCE(jsonb_agg(assignment.location_id::text)
			           FILTER (WHERE assignment.id IS NOT NULL), '[]'::JSONB),
			       CASE
			           WHEN invite.id IS NULL THEN ''
			           WHEN invite.expires_at > now() THEN 'pending'
			           ELSE 'expired'
			       END
			FROM tenant_memberships membership
			JOIN users user_account ON user_account.id = membership.user_id
			LEFT JOIN user_location_assignments assignment
			  ON assignment.tenant_id = membership.tenant_id
			 AND assignment.user_id = membership.user_id
			 AND assignment.revoked_at IS NULL
			LEFT JOIN staff_invites invite
			  ON invite.tenant_id = membership.tenant_id
			 AND invite.user_id = membership.user_id
			 AND invite.accepted_at IS NULL
			WHERE membership.tenant_id = $1
			  AND ($2 OR membership.user_id = $3)
			GROUP BY membership.user_id, user_account.name, user_account.email,
			         membership.role, membership.created_at,
			         invite.id, invite.expires_at
			ORDER BY membership.created_at, membership.user_id`,
			tenantID, page.CanManage, actorUserID)
		if err != nil {
			return err
		}
		defer memberRows.Close()
		for memberRows.Next() {
			var member Member
			var locationIDsJSON []byte
			if err := memberRows.Scan(
				&member.UserID, &member.Name, &member.Email,
				&member.Role, &locationIDsJSON, &member.InviteState,
			); err != nil {
				return err
			}
			if err := json.Unmarshal(locationIDsJSON, &member.LocationIDs); err != nil {
				return err
			}
			slices.Sort(member.LocationIDs)
			page.Members = append(page.Members, member)
		}
		return memberRows.Err()
	})
	return page, err
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
		if err := requireStaffTarget(ctx, tx, tenantID, targetUserID); err != nil {
			return err
		}
		return syncLocationAssignments(
			ctx, tx, tenantID, actorUserID, targetUserID, desiredLocationIDs,
		)
	})
}

// syncLocationAssignments makes the target's live assignments match desired
// exactly, revoking what is dropped rather than deleting it so the audit trail
// survives. The caller has already authorized the change.
func syncLocationAssignments(
	ctx context.Context,
	tx pgx.Tx,
	tenantID string,
	actorUserID string,
	targetUserID string,
	desiredLocationIDs []string,
) error {
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
}

// InviteStaff enrols an employee the owner names, and returns the single-use
// code they will type. The employee gets an account without ever owning an
// email address or a password: everything they need is in the token.
//
// The whole enrolment is one transaction under the owner's identity, so the
// policies from migration 0029 are what authorize it — a member running the
// same statements is refused by PostgreSQL, not by this function.
func (s *Store) InviteStaff(
	ctx context.Context,
	tenantID string,
	actorUserID string,
	name string,
	locationIDs []string,
) (invite StaffInvite, err error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return StaffInvite{}, ErrNameRequired
	}
	err = s.db.WithinTenantUser(ctx, tenantID, actorUserID, func(tx pgx.Tx) error {
		if err := requireAdministrator(ctx, tx, tenantID, actorUserID); err != nil {
			return err
		}
		// provider_id is random rather than derived: an invited employee has no
		// external identity to key on, and users is unique on (provider,
		// provider_id). The empty email keeps them out of the unique email
		// index until they ever have one.
		if err := tx.QueryRow(ctx, `
			INSERT INTO users (provider, provider_id, email, name)
			VALUES ('invite', $1, '', $2)
			RETURNING id`, rand.Text(), name,
		).Scan(&invite.UserID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO tenant_memberships (tenant_id, user_id, role)
			VALUES ($1, $2, 'member')`, tenantID, invite.UserID,
		); err != nil {
			return err
		}
		if err := syncLocationAssignments(
			ctx, tx, tenantID, actorUserID, invite.UserID, locationIDs,
		); err != nil {
			return err
		}
		invite.Token, err = issueInvite(ctx, tx, tenantID, actorUserID, invite.UserID)
		return err
	})
	if err != nil {
		return StaffInvite{}, err
	}
	return invite, nil
}

// ReissueInvite mints a fresh code for someone already on the team. It is the
// answer to the two things that actually happen in a garage: the employee needs
// to sign in on a second screen, and someone lost the code. Any previous
// pending code stops working, so only one is ever in circulation.
func (s *Store) ReissueInvite(
	ctx context.Context,
	tenantID string,
	actorUserID string,
	targetUserID string,
) (invite StaffInvite, err error) {
	invite.UserID = targetUserID
	err = s.db.WithinTenantUser(ctx, tenantID, actorUserID, func(tx pgx.Tx) error {
		if err := requireAdministrator(ctx, tx, tenantID, actorUserID); err != nil {
			return err
		}
		// Owners and admins sign in with their own identity provider; handing
		// one of them a staff code would be a way to take their seat.
		if err := requireStaffTarget(ctx, tx, tenantID, targetUserID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			DELETE FROM staff_invites
			WHERE tenant_id = $1 AND user_id = $2 AND accepted_at IS NULL`,
			tenantID, targetUserID,
		); err != nil {
			return err
		}
		invite.Token, err = issueInvite(ctx, tx, tenantID, actorUserID, targetUserID)
		return err
	})
	if err != nil {
		return StaffInvite{}, err
	}
	return invite, nil
}

// RenameStaff fixes a name the owner typed wrong. Without it the only cure for
// a typo is removing the person and enrolling them again, which costs them
// their access and their sites.
//
// Only invited staff can be renamed. An owner or admin signed in through an
// identity provider has their name refreshed from it at every login, so editing
// it here would quietly revert — better to refuse than to pretend.
//
// users carries no row security (it is an identity table, like sessions), so
// the authorization below is the whole guard, not a second line of defence.
func (s *Store) RenameStaff(
	ctx context.Context,
	tenantID string,
	actorUserID string,
	targetUserID string,
	name string,
) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return ErrNameRequired
	}
	return s.db.WithinTenantUser(ctx, tenantID, actorUserID, func(tx pgx.Tx) error {
		if err := requireAdministrator(ctx, tx, tenantID, actorUserID); err != nil {
			return err
		}
		if err := requireStaffTarget(ctx, tx, tenantID, targetUserID); err != nil {
			return err
		}
		result, err := tx.Exec(ctx, `
			UPDATE users SET name = $2, updated_at = now()
			WHERE id = $1 AND provider = 'invite'`, targetUserID, name)
		if err != nil {
			return err
		}
		if result.RowsAffected() != 1 {
			return ErrForbidden
		}
		return nil
	})
}

// RevokeStaff removes someone from the organization. Their pending invitation
// goes with the membership through the foreign key, and their sessions are
// dropped back to "no workspace" rather than deleted, so revoking someone here
// cannot sign them out of another garage they legitimately belong to.
func (s *Store) RevokeStaff(
	ctx context.Context,
	tenantID string,
	actorUserID string,
	targetUserID string,
) error {
	// Refusing this here as well as in RLS is not belt and braces for its own
	// sake: the policy reports a blocked delete as zero rows, which reads as
	// "no such member", and a request to remove yourself deserves a clearer
	// answer than a 404.
	if targetUserID == actorUserID {
		return ErrForbidden
	}
	return s.db.WithinTenantUser(ctx, tenantID, actorUserID, func(tx pgx.Tx) error {
		if err := requireAdministrator(ctx, tx, tenantID, actorUserID); err != nil {
			return err
		}
		// Owners and admins run the organization; taking one out is not the
		// routine staff change this screen offers, so it is refused rather than
		// half-answered. The team screen hides the button for them too.
		if err := requireStaffTarget(ctx, tx, tenantID, targetUserID); err != nil {
			return err
		}
		result, err := tx.Exec(ctx, `
			DELETE FROM tenant_memberships
			WHERE tenant_id = $1 AND user_id = $2`, tenantID, targetUserID)
		if err != nil {
			return err
		}
		if result.RowsAffected() != 1 {
			return sql.ErrNoRows
		}
		_, err = tx.Exec(ctx, `
			UPDATE sessions
			SET active_tenant_id = NULL, active_location_id = NULL
			WHERE user_id = $1 AND active_tenant_id = $2`, targetUserID, tenantID)
		return err
	})
}

// issueInvite writes one pending invitation and hands back the only copy of its
// raw code. Callers have already authorized the change.
func issueInvite(
	ctx context.Context,
	tx pgx.Tx,
	tenantID string,
	actorUserID string,
	targetUserID string,
) (string, error) {
	token := rand.Text()[:inviteCodeLength]
	_, err := tx.Exec(ctx, `
		INSERT INTO staff_invites (
			tenant_id, user_id, token_hash, created_by_user_id, expires_at
		) VALUES ($1, $2, $3, $4, $5)`,
		tenantID, targetUserID, hashToken(token), actorUserID,
		time.Now().UTC().Add(inviteTTL),
	)
	if err != nil {
		return "", err
	}
	return token, nil
}

// hashToken is how an invitation token is stored. Whoever accepts the
// invitation (features/auth) hashes the value from the URL the same way.
func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// requireStaffTarget refuses every change this screen offers when it is aimed
// at someone who runs the business. Reading the role through tenant_memberships
// is also what proves the target belongs to this garage: users is global.
func requireStaffTarget(
	ctx context.Context,
	tx pgx.Tx,
	tenantID string,
	targetUserID string,
) error {
	var role string
	if err := tx.QueryRow(ctx, `
		SELECT role FROM tenant_memberships
		WHERE tenant_id = $1 AND user_id = $2`, tenantID, targetUserID,
	).Scan(&role); err != nil {
		return err
	}
	if role == RoleOwner || role == RoleAdmin {
		return ErrForbidden
	}
	return nil
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
