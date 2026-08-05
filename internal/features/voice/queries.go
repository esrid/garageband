package voice

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/esrid/garageband/internal/platform/db"
)

type Store struct{ db *db.DB }

func NewStore(database *db.DB) *Store { return &Store{db: database} }

// Route is everything a call needs to know about itself before it is
// answered. None of it is looked up: each number's webhook URL carries it, and
// Twilio signs that URL in full, so the signature proving the request came from
// Twilio also proves the ids were not swapped on the way in.
//
// Carrying the agent and the number rather than resolving them is what keeps
// row security intact. The runtime writes a call with no user signed in, and
// every read policy on agents and phone_numbers asks which locations the
// current user may reach — a question with no answer mid-call. Reading nothing
// means the runtime needs no read policy of its own.
type Route struct {
	TenantID   string
	LocationID string
	AgentID    string
	NumberID   string
}

// complete reports whether a route names everything a call row needs. A number
// whose webhook URL is missing any of it is one we did not provision.
func (r Route) complete() bool {
	return r.TenantID != "" && r.LocationID != "" && r.AgentID != "" && r.NumberID != ""
}

// StartCall opens the call's own record the moment the socket does. It runs
// with a tenant and no user, which is what the runtime policies in migration
// 0032 recognise: the caller is anonymous and no employee is signed in.
//
// The provider's call id is unique per tenant, so a socket that reconnects for
// the same call finds its row again instead of opening a second one.
func (s *Store) StartCall(
	ctx context.Context, route Route, providerCallID, fromE164, toE164 string,
	startedAt time.Time,
) (callID string, err error) {
	err = s.db.WithinTenant(ctx, route.TenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			INSERT INTO calls (
			    tenant_id, location_id, agent_id, phone_number_id,
			    provider_call_id, direction, status,
			    from_e164, to_e164, started_at, answered_at
			)
			VALUES ($1, $2, $3, $4, $5, 'inbound', 'in_progress', $6, $7, $8, $8)
			ON CONFLICT (tenant_id, provider_call_id) DO UPDATE
			SET updated_at = now()
			RETURNING id`,
			route.TenantID, route.LocationID, route.AgentID, route.NumberID,
			providerCallID, fromE164, toE164, startedAt,
		).Scan(&callID)
	})
	return callID, err
}

// AppendTurns writes what was said, in order. The sequence continues from
// what is already stored rather than from a counter held in memory, so a
// reconnected socket cannot collide with its own earlier turns.
func (s *Store) AppendTurns(
	ctx context.Context, tenantID, callID string, turns []Turn, occurredAt time.Time,
) error {
	if len(turns) == 0 {
		return nil
	}
	return s.db.WithinTenant(ctx, tenantID, func(tx pgx.Tx) error {
		var next int
		if err := tx.QueryRow(ctx, `
			SELECT COALESCE(MAX(sequence) + 1, 0) FROM call_messages
			WHERE tenant_id = $1 AND call_id = $2`,
			tenantID, callID,
		).Scan(&next); err != nil {
			return err
		}
		for _, turn := range turns {
			text := strings.TrimSpace(turn.Text)
			if text == "" {
				continue
			}
			if _, err := tx.Exec(ctx, `
				INSERT INTO call_messages (
				    tenant_id, call_id, sequence, speaker, content, occurred_at
				)
				VALUES ($1, $2, $3, $4, $5, $6)`,
				tenantID, callID, next, turn.Role, text, occurredAt,
			); err != nil {
				return err
			}
			next++
		}
		return nil
	})
}

// EndCall closes the record. status is the carrier's own vocabulary, already
// constrained by the table: completed, failed, no_answer and the rest.
func (s *Store) EndCall(
	ctx context.Context, tenantID, callID, status string, endedAt time.Time,
) error {
	return s.db.WithinTenant(ctx, tenantID, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `
			UPDATE calls
			SET status = $3, ended_at = $4, updated_at = now()
			WHERE tenant_id = $1 AND id = $2 AND ended_at IS NULL`,
			tenantID, callID, status, endedAt)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return sql.ErrNoRows
		}
		return nil
	})
}

// Account is a garage's carrier subaccount.
type Account struct {
	SubaccountSID string
	Status        string
}

// Account returns the organization's subaccount, if it has one yet.
func (s *Store) Account(ctx context.Context, tenantID, userID string) (Account, bool, error) {
	var account Account
	found := false
	err := s.db.WithinTenantUser(ctx, tenantID, userID, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `
			SELECT subaccount_sid, status FROM telephony_accounts
			WHERE tenant_id = $1`,
			tenantID,
		).Scan(&account.SubaccountSID, &account.Status)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		found = true
		return nil
	})
	return account, found, err
}

// LinkAccount records the subaccount created for this organization. One
// subaccount per organization is enforced by the table, so a second attempt
// after a crashed provisioning run fails loudly instead of splitting a garage
// across two carrier accounts.
func (s *Store) LinkAccount(ctx context.Context, tenantID, userID, subaccountSID string) error {
	return s.db.WithinTenantUser(ctx, tenantID, userID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			INSERT INTO telephony_accounts (tenant_id, subaccount_sid)
			VALUES ($1, $2)`,
			tenantID, subaccountSID)
		return err
	})
}

// Bundle is a compliance file as the carrier sees it.
type Bundle struct {
	ID            string
	SID           string
	ISOCountry    string
	NumberType    string
	Status        string
	FailureReason string
	SubmittedAt   *time.Time
	ReviewedAt    *time.Time
}

// RecordBundle stores a bundle the moment it is submitted, so the wait itself
// is measurable: this timestamp minus the review's is the time to activation
// the order form promises.
func (s *Store) RecordBundle(
	ctx context.Context, tenantID, userID string, bundle Bundle,
) (id string, err error) {
	err = s.db.WithinTenantUser(ctx, tenantID, userID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			INSERT INTO regulatory_bundles (
			    tenant_id, bundle_sid, iso_country, number_type, status, submitted_at
			)
			VALUES ($1, $2, $3, $4, $5, $6)
			RETURNING id`,
			tenantID, bundle.SID, bundle.ISOCountry, bundle.NumberType,
			bundle.Status, bundle.SubmittedAt,
		).Scan(&id)
	})
	return id, err
}

// SyncBundleStatus applies what the carrier now says about a bundle. A reason
// only survives on a rejection, which the table enforces rather than trusting
// the caller to blank it.
func (s *Store) SyncBundleStatus(
	ctx context.Context, tenantID, userID, bundleSID, status, failureReason string,
	reviewedAt *time.Time,
) error {
	if status != "twilio-rejected" {
		failureReason = ""
	}
	var reason *string
	if failureReason != "" {
		reason = &failureReason
	}
	return s.db.WithinTenantUser(ctx, tenantID, userID, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `
			UPDATE regulatory_bundles
			SET status = $3, failure_reason = $4, reviewed_at = $5, updated_at = now()
			WHERE tenant_id = $1 AND bundle_sid = $2`,
			tenantID, bundleSID, status, reason, reviewedAt)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return sql.ErrNoRows
		}
		return nil
	})
}

// Number is a phone number held for a site. It lives in phone_numbers, the
// table the agents screen already reads from, so a provisioned number and a
// seeded one are the same row to everything downstream.
type Number struct {
	ID           string
	LocationID   string
	ConnectionID string
	BundleID     string
	E164         string
	// ProviderSID is the carrier's own id for the number.
	ProviderSID string
	// WhatsAppSender records that this number also carries the WhatsApp
	// sender. In France it never carries the SMS: A2P messages go out under an
	// Alphanumeric Sender ID, which is a name and not a number.
	WhatsAppSender bool
}

// AttachNumber records a number bought for a site, before it answers anything.
// It lands as 'provisioning': a number exists at the carrier well before its
// webhooks are wired, and the gap is a real state rather than a lie about
// being active.
func (s *Store) AttachNumber(
	ctx context.Context, tenantID, userID string, number Number,
) (id string, err error) {
	err = s.db.WithinTenantUser(ctx, tenantID, userID, func(tx pgx.Tx) error {
		var bundleID *string
		if number.BundleID != "" {
			bundleID = &number.BundleID
		}
		return tx.QueryRow(ctx, `
			INSERT INTO phone_numbers (
			    tenant_id, location_id, telephony_connection_id, bundle_id,
			    phone_e164, external_number_id, whatsapp_sender, status
			)
			VALUES ($1, $2, $3, $4, $5, $6, $7, 'provisioning')
			RETURNING id`,
			tenantID, number.LocationID, number.ConnectionID, bundleID,
			number.E164, number.ProviderSID, number.WhatsAppSender,
		).Scan(&id)
	})
	return id, err
}

// ActivateNumber opens a number to real calls, once its webhooks point here.
func (s *Store) ActivateNumber(ctx context.Context, tenantID, userID, numberID string) error {
	return s.db.WithinTenantUser(ctx, tenantID, userID, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `
			UPDATE phone_numbers
			SET status = 'active', updated_at = now()
			WHERE tenant_id = $1 AND id = $2 AND released_at IS NULL`,
			tenantID, numberID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return sql.ErrNoRows
		}
		return nil
	})
}

// ReleaseNumber gives a number back. The row stays for its history and the
// partial unique index frees the E.164 immediately, so the same number can be
// bought again — by this garage or the next — without a separate cleanup step.
func (s *Store) ReleaseNumber(ctx context.Context, tenantID, userID, numberID string) error {
	return s.db.WithinTenantUser(ctx, tenantID, userID, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `
			UPDATE phone_numbers
			SET status = 'released', released_at = now(), updated_at = now()
			WHERE tenant_id = $1 AND id = $2 AND released_at IS NULL`,
			tenantID, numberID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return sql.ErrNoRows
		}
		return nil
	})
}
