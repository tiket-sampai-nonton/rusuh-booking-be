package outbound

import (
	"context"
	"time"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/user/internal/user/domain/entity"
)

//go:generate go tool mockgen -source=repositories.go -destination=../../../../_mock/user/port/outbound/repositories_mock.go -package=mock_outbound

// IAccountRepository persists accounts (schema users).
type IAccountRepository interface {
	FindByEmailHash(ctx context.Context, emailHash []byte) (entity.Account, bool, error)
	Create(ctx context.Context, a entity.NewAccount) (entity.Account, error)
	GetByID(ctx context.Context, id string) (entity.Account, error)
	UpdateProfile(ctx context.Context, id string, upd entity.ProfileUpdate) (entity.Account, error)
	TouchLogin(ctx context.Context, id string, at time.Time) error
}

// ISessionRepository persists refresh-token sessions.
type ISessionRepository interface {
	Create(ctx context.Context, s entity.NewSession) (entity.Session, error)
	FindByTokenHash(ctx context.Context, hash []byte) (entity.Session, bool, error)
	// Rotate revokes oldID and stores next atomically; entity.ErrRevoked bila oldID sudah dicabut.
	Rotate(ctx context.Context, oldID string, next entity.NewSession, at time.Time) (entity.Session, error)
	Revoke(ctx context.Context, id string, at time.Time) error
	RevokeAllForAccount(ctx context.Context, accountID string, at time.Time) error
}

// ILoginAudit records login events (users.login_audit).
type ILoginAudit interface {
	Record(ctx context.Context, e entity.LoginEvent) error
}
