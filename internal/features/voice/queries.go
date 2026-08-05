package voice

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/esrid/garageband/internal/platform/db"
)

type Store struct{ db *db.DB }

func NewStore(database *db.DB) *Store { return &Store{db: database} }

// Route is the site a call belongs to. It is not looked up when the call
// arrives: the webhook URL of each number carries it, and Twilio signs that
// URL in full, so the signature that proves the request came from Twilio also
// proves the site was not swapped on the way in. No query, no row of tenant
// data read without a tenant context, and one fewer table read on every call.
type Route struct {
	TenantID   string
	LocationID string
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
