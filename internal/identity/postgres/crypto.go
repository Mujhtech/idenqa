package postgres

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"

	"github.com/Mujhtech/idenqa/internal/identity"
	platformcrypto "github.com/Mujhtech/idenqa/internal/platform/crypto"
	"github.com/Mujhtech/idenqa/internal/platform/kms"
	pg "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/tenant"
	"github.com/jackc/pgx/v5"
)

func contextBytes(domain, tenantID, region, reference string) ([]byte, error) {
	return json.Marshal([]string{domain, tenantID, region, reference, "1"})
}
func token(key []byte, parts ...string) (string, error) {
	b, e := json.Marshal(parts)
	if e != nil {
		return "", e
	}
	defer clear(b)
	mac := hmac.New(sha256.New, key)
	if _, e = mac.Write(b); e != nil {
		return "", e
	}
	return hex.EncodeToString(mac.Sum(nil)), nil
}
func (s *Store) wrapNew(ctx context.Context, domain, tenantID, region, reference string) ([]byte, []byte, error) {
	wrapper, ok := s.keys.(platformcrypto.KeyWrapper)
	if !ok {
		return nil, nil, identity.ErrUnavailable
	}
	key := make([]byte, 32)
	if _, e := rand.Read(key); e != nil {
		return nil, nil, e
	}
	purpose, e := kms.NewPurpose(domain)
	if e != nil {
		clear(key)
		return nil, nil, e
	}
	aad, e := contextBytes(domain, tenantID, region, reference)
	if e != nil {
		clear(key)
		return nil, nil, e
	}
	wrapped, e := wrapper.Wrap(ctx, purpose, key, aad)
	if e != nil {
		clear(key)
		return nil, nil, e
	}
	b, e := json.Marshal(wrapped.Record())
	if e != nil {
		clear(key)
		return nil, nil, e
	}
	return key, b, nil
}
func (s *Store) unwrap(ctx context.Context, b []byte, domain, tenantID, region, reference string) ([]byte, error) {
	if s.keys == nil || len(b) == 0 {
		return nil, identity.ErrUnavailable
	}
	var record kms.WrappedKeyRecord
	if e := json.Unmarshal(b, &record); e != nil {
		return nil, identity.ErrUnavailable
	}
	wrapped, e := kms.NewWrappedKey(record)
	if e != nil {
		return nil, e
	}
	purpose, e := kms.NewPurpose(domain)
	if e != nil {
		return nil, e
	}
	aad, e := contextBytes(domain, tenantID, region, reference)
	if e != nil {
		return nil, e
	}
	key, e := s.keys.Unwrap(ctx, purpose, wrapped, aad)
	if e != nil {
		return nil, e
	}
	if len(key) != 32 {
		clear(key)
		return nil, identity.ErrUnavailable
	}
	return key, nil
}
func (s *Store) lookupKey(ctx context.Context, tx pg.Transaction, scope tenant.Scope, region string, create bool) ([]byte, error) {
	var wrapped []byte
	e := tx.QueryRow(ctx, `SELECT wrapped_key FROM idenqa.identity_keys WHERE tenant_id=$1 AND region=$2`, scope.ID().String(), region).Scan(&wrapped)
	if errors.Is(e, pgx.ErrNoRows) && create {
		key, b, err := s.wrapNew(ctx, "identity.lookup.v1", scope.ID().String(), region, "lookup")
		clear(key)
		if err != nil {
			return nil, err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO idenqa.identity_keys(tenant_id,region,wrapped_key)VALUES($1,$2,$3) ON CONFLICT DO NOTHING`, scope.ID().String(), region, b); err != nil {
			return nil, err
		}
		e = tx.QueryRow(ctx, `SELECT wrapped_key FROM idenqa.identity_keys WHERE tenant_id=$1 AND region=$2`, scope.ID().String(), region).Scan(&wrapped)
	}
	if errors.Is(e, pgx.ErrNoRows) {
		return nil, identity.ErrNotFound
	}
	if e != nil {
		return nil, e
	}
	return s.unwrap(ctx, wrapped, "identity.lookup.v1", scope.ID().String(), region, "lookup")
}
func sealValue(key, aad, plain []byte) ([]byte, error) {
	block, e := aes.NewCipher(key)
	if e != nil {
		return nil, e
	}
	aead, e := cipher.NewGCM(block)
	if e != nil {
		return nil, e
	}
	nonce := make([]byte, aead.NonceSize())
	if _, e = rand.Read(nonce); e != nil {
		return nil, e
	}
	return aead.Seal(nonce, nonce, plain, aad), nil
}
func openValue(key, aad, sealed []byte) ([]byte, error) {
	block, e := aes.NewCipher(key)
	if e != nil {
		return nil, e
	}
	aead, e := cipher.NewGCM(block)
	if e != nil {
		return nil, e
	}
	if len(sealed) < aead.NonceSize()+aead.Overhead() || len(sealed) > 32*1024 {
		return nil, identity.ErrUnavailable
	}
	plain, e := aead.Open(nil, sealed[:aead.NonceSize()], sealed[aead.NonceSize():], aad)
	if e != nil {
		return nil, identity.ErrUnavailable
	}
	return plain, nil
}
