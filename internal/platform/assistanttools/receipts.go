package assistanttools

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"

	"github.com/esrid/garageband/internal/platform/db"
)

// WithReceipt runs perform inside the same transaction as the idempotency
// receipt check-and-record, so a retried Execute call (network blip, model
// retry) neither repeats the write nor invents a second answer: the receipt
// from the first attempt short-circuits every later call with the same key,
// returning exactly what the first one returned.
//
// The check and the write share one transaction on purpose. Split apart, two
// concurrent retries could both miss the receipt and both perform.
func WithReceipt(
	ctx context.Context,
	database *db.DB,
	scope Scope,
	toolName string,
	perform func(tx pgx.Tx) (json.RawMessage, []AffectedRecord, error),
) (output json.RawMessage, affected []AffectedRecord, err error) {
	err = database.WithinTenantUser(ctx, scope.TenantID, scope.UserID, func(tx pgx.Tx) error {
		var receiptOutput, receiptAffected []byte
		err := tx.QueryRow(ctx, `
			SELECT output, affected_records
			FROM application_tool_receipts
			WHERE tenant_id = $1 AND location_id = $2 AND idempotency_key = $3
			  AND tool_name = $4`, scope.TenantID, scope.LocationID,
			scope.IdempotencyKey, toolName,
		).Scan(&receiptOutput, &receiptAffected)
		if err == nil {
			output = receiptOutput
			return json.Unmarshal(receiptAffected, &affected)
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		output, affected, err = perform(tx)
		if err != nil {
			return err
		}
		affectedJSON, err := json.Marshal(affected)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `
			INSERT INTO application_tool_receipts (
			    tenant_id, location_id, idempotency_key, tool_name, output, affected_records
			) VALUES ($1, $2, $3, $4, $5, $6)`,
			scope.TenantID, scope.LocationID, scope.IdempotencyKey, toolName, output, affectedJSON)
		return err
	})
	return output, affected, err
}
