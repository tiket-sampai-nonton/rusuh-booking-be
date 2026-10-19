// Package postgres implements the User repositories on schema users (pgx).
package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	pkgpg "github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/postgres"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/user/internal/user/domain/entity"
)

const accountCols = `id::text, email_masked, coalesce(full_name, ''), coalesce(nik_masked, ''), status, created_at, last_login_at`

func scanAccount(row pgx.Row) (entity.Account, error) {
	var a entity.Account
	err := row.Scan(&a.ID, &a.EmailMasked, &a.FullName, &a.NIKMasked, &a.Status, &a.CreatedAt, &a.LastLoginAt)
	return a, err
}

// Accounts implements outbound.IAccountRepository.
type Accounts struct{ pool *pgxpool.Pool }

// NewAccounts returns the repository.
func NewAccounts(pool *pgxpool.Pool) *Accounts { return &Accounts{pool: pool} }

// FindByEmailHash looks up an account by email hash.
func (r *Accounts) FindByEmailHash(ctx context.Context, emailHash []byte) (entity.Account, bool, error) {
	a, err := scanAccount(r.pool.QueryRow(ctx, `SELECT `+accountCols+` FROM accounts WHERE email_hash = $1`, emailHash))
	if errors.Is(err, pgx.ErrNoRows) {
		return entity.Account{}, false, nil
	}
	if err != nil {
		return entity.Account{}, false, fmt.Errorf("accounts: find: %w", err)
	}
	return a, true, nil
}

// Create inserts an account; login bersamaan untuk email yang sama mengembalikan akun yang sudah ada.
func (r *Accounts) Create(ctx context.Context, n entity.NewAccount) (entity.Account, error) {
	a, err := scanAccount(r.pool.QueryRow(ctx, `
		INSERT INTO accounts (email_hash, email_enc, email_masked) VALUES ($1, $2, $3)
		ON CONFLICT (email_hash) DO UPDATE SET email_hash = EXCLUDED.email_hash
		RETURNING `+accountCols, n.EmailHash, n.EmailEnc, n.EmailMasked))
	if err != nil {
		return entity.Account{}, fmt.Errorf("accounts: create: %w", err)
	}
	return a, nil
}

// GetByID returns the account or entity.ErrNotFound.
func (r *Accounts) GetByID(ctx context.Context, id string) (entity.Account, error) {
	a, err := scanAccount(r.pool.QueryRow(ctx, `SELECT `+accountCols+` FROM accounts WHERE id = $1::uuid`, id))
	if errors.Is(err, pgx.ErrNoRows) || pkgpg.IsCode(err, "22P02") {
		return entity.Account{}, entity.ErrNotFound
	}
	if err != nil {
		return entity.Account{}, fmt.Errorf("accounts: get: %w", err)
	}
	return a, nil
}

// UpdateProfile changes full name and/or NIK. NIK yang sudah tertaut ke akun lain → entity.ErrNIKTaken.
func (r *Accounts) UpdateProfile(ctx context.Context, id string, u entity.ProfileUpdate) (entity.Account, error) {
	a, err := scanAccount(r.pool.QueryRow(ctx, `
		UPDATE accounts SET
		  full_name  = CASE WHEN $2::boolean THEN $3 ELSE full_name END,
		  nik_hash   = CASE WHEN $4::boolean THEN $5 ELSE nik_hash END,
		  nik_enc    = CASE WHEN $4::boolean THEN $6 ELSE nik_enc END,
		  nik_masked = CASE WHEN $4::boolean THEN $7 ELSE nik_masked END,
		  updated_at = now()
		WHERE id = $1::uuid
		RETURNING `+accountCols,
		id, u.FullName != nil, u.FullName, u.NIKHash != nil, u.NIKHash, u.NIKEnc, u.NIKMasked))
	switch {
	case errors.Is(err, pgx.ErrNoRows), pkgpg.IsCode(err, "22P02"):
		return entity.Account{}, entity.ErrNotFound
	case pkgpg.IsCode(err, pkgpg.CodeUniqueViolation) && pkgpg.ConstraintName(err) == "uq_accounts_nik":
		return entity.Account{}, entity.ErrNIKTaken
	case err != nil:
		return entity.Account{}, fmt.Errorf("accounts: update: %w", err)
	}
	return a, nil
}

// TouchLogin records the last login time.
func (r *Accounts) TouchLogin(ctx context.Context, id string, at time.Time) error {
	_, err := r.pool.Exec(ctx, `UPDATE accounts SET last_login_at = $2 WHERE id = $1::uuid`, id, at)
	return err
}

// Sessions implements outbound.ISessionRepository.
type Sessions struct{ pool *pgxpool.Pool }

// NewSessions returns the repository.
func NewSessions(pool *pgxpool.Pool) *Sessions { return &Sessions{pool: pool} }

const sessionCols = `id::text, account_id::text, expires_at, revoked_at`

func scanSession(row pgx.Row) (entity.Session, error) {
	var s entity.Session
	err := row.Scan(&s.ID, &s.AccountID, &s.ExpiresAt, &s.RevokedAt)
	return s, err
}

// Create stores a session.
func (r *Sessions) Create(ctx context.Context, n entity.NewSession) (entity.Session, error) {
	s, err := scanSession(r.pool.QueryRow(ctx, `
		INSERT INTO sessions (account_id, refresh_token_hash, user_agent, expires_at) VALUES ($1::uuid, $2, $3, $4)
		RETURNING `+sessionCols, n.AccountID, n.RefreshTokenHash, n.UserAgent, n.ExpiresAt))
	if err != nil {
		return entity.Session{}, fmt.Errorf("sessions: create: %w", err)
	}
	return s, nil
}

// FindByTokenHash returns the session for a refresh token hash.
func (r *Sessions) FindByTokenHash(ctx context.Context, hash []byte) (entity.Session, bool, error) {
	s, err := scanSession(r.pool.QueryRow(ctx, `SELECT `+sessionCols+` FROM sessions WHERE refresh_token_hash = $1`, hash))
	if errors.Is(err, pgx.ErrNoRows) {
		return entity.Session{}, false, nil
	}
	if err != nil {
		return entity.Session{}, false, fmt.Errorf("sessions: find: %w", err)
	}
	return s, true, nil
}

// Rotate revokes oldID and inserts next in one transaction. Dua refresh bersamaan dengan token yang sama:
// hanya satu yang menang, yang lain mendapat entity.ErrRevoked.
func (r *Sessions) Rotate(ctx context.Context, oldID string, next entity.NewSession, at time.Time) (entity.Session, error) {
	var out entity.Session
	err := pkgpg.WithTx(ctx, r.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		s, err := scanSession(tx.QueryRow(ctx, `
			INSERT INTO sessions (account_id, refresh_token_hash, user_agent, expires_at) VALUES ($1::uuid, $2, $3, $4)
			RETURNING `+sessionCols, next.AccountID, next.RefreshTokenHash, next.UserAgent, next.ExpiresAt))
		if err != nil {
			return fmt.Errorf("sessions: rotate insert: %w", err)
		}
		tag, err := tx.Exec(ctx, `UPDATE sessions SET revoked_at = $2, replaced_by = $3::uuid WHERE id = $1::uuid AND revoked_at IS NULL`, oldID, at, s.ID)
		if err != nil {
			return fmt.Errorf("sessions: rotate revoke: %w", err)
		}
		if tag.RowsAffected() != 1 {
			return entity.ErrRevoked
		}
		out = s
		return nil
	})
	return out, err
}

// Revoke revokes one session (idempoten).
func (r *Sessions) Revoke(ctx context.Context, id string, at time.Time) error {
	_, err := r.pool.Exec(ctx, `UPDATE sessions SET revoked_at = $2 WHERE id = $1::uuid AND revoked_at IS NULL`, id, at)
	return err
}

// RevokeAllForAccount revokes every active session of the account.
func (r *Sessions) RevokeAllForAccount(ctx context.Context, accountID string, at time.Time) error {
	_, err := r.pool.Exec(ctx, `UPDATE sessions SET revoked_at = $2 WHERE account_id = $1::uuid AND revoked_at IS NULL`, accountID, at)
	return err
}

// Audit implements outbound.ILoginAudit.
type Audit struct{ pool *pgxpool.Pool }

// NewAudit returns the audit writer.
func NewAudit(pool *pgxpool.Pool) *Audit { return &Audit{pool: pool} }

// Record inserts one login_audit row.
func (r *Audit) Record(ctx context.Context, e entity.LoginEvent) error {
	var account, ip any
	if e.AccountID != "" {
		account = e.AccountID
	}
	if e.ClientIP != "" {
		ip = e.ClientIP
	}
	emailHash := e.EmailHash
	if emailHash == nil {
		emailHash = []byte{}
	}
	_, err := r.pool.Exec(ctx, `INSERT INTO login_audit (account_id, email_hash, outcome, client_ip) VALUES ($1::uuid, $2, $3, $4::inet)`,
		account, emailHash, e.Outcome, ip)
	return err
}
