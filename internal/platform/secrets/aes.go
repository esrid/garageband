package secrets

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
	"io"

	"github.com/jackc/pgx/v5"
)

// AESStore implements Store with AES-256-GCM at the application layer: the
// database only ever stores ciphertext and its nonce, and the key never
// touches the database process. Not pgcrypto, so no new extension.
type AESStore struct {
	gcm cipher.AEAD
}

// NewAESStore builds an AESStore from a 32-byte AES-256 key.
func NewAESStore(key []byte) (*AESStore, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("secrets: encryption key must be 32 bytes, got %d", len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &AESStore{gcm: gcm}, nil
}

func (s *AESStore) Store(
	ctx context.Context, tx pgx.Tx, tenantID string, plaintext []byte,
) (reference string, err error) {
	nonce := make([]byte, s.gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	ciphertext := s.gcm.Seal(nil, nonce, plaintext, nil)
	err = tx.QueryRow(ctx, `
		INSERT INTO encrypted_secrets (tenant_id, ciphertext, nonce)
		VALUES ($1, $2, $3) RETURNING id::text`,
		tenantID, ciphertext, nonce,
	).Scan(&reference)
	return reference, err
}

func (s *AESStore) Resolve(ctx context.Context, tx pgx.Tx, reference string) ([]byte, error) {
	var ciphertext, nonce []byte
	if err := tx.QueryRow(ctx, `
		SELECT ciphertext, nonce FROM encrypted_secrets WHERE id = $1`,
		reference,
	).Scan(&ciphertext, &nonce); err != nil {
		return nil, err
	}
	plaintext, err := s.gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, errors.Join(errors.New("secrets: decryption failed"), err)
	}
	return plaintext, nil
}

func (s *AESStore) Delete(ctx context.Context, tx pgx.Tx, reference string) error {
	_, err := tx.Exec(ctx, `DELETE FROM encrypted_secrets WHERE id = $1`, reference)
	return err
}
