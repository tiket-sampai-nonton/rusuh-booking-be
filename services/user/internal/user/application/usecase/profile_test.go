package usecase

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/apperror"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/clock"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/revocation"

	mock_outbound "github.com/tiket-sampai-nonton/rusuh-booking-be/services/user/_mock/user/port/outbound"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/user/internal/user/domain/dto"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/user/internal/user/domain/entity"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/user/internal/user/port/inbound"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/user/internal/user/port/outbound"
)

func ptr(s string) *string { return &s }

const testAccessTTL = 15 * time.Minute

func newProfile(t *testing.T) (*Profile, *mock_outbound.MockIAccountRepository, *mock_outbound.MockIPIIProtector) {
	u, repo, pii, _, _ := newProfileDeps(t)
	return u, repo, pii
}

// newProfileDeps also returns the limiter mock and the denylist fake the NIK change path uses.
func newProfileDeps(t *testing.T) (*Profile, *mock_outbound.MockIAccountRepository, *mock_outbound.MockIPIIProtector, *mock_outbound.MockIRateLimiter, *fakeDenylist) {
	ctrl := gomock.NewController(t)
	repo := mock_outbound.NewMockIAccountRepository(ctrl)
	pii := mock_outbound.NewMockIPIIProtector(ctrl)
	limiter := mock_outbound.NewMockIRateLimiter(ctrl)
	deny := &fakeDenylist{}
	return NewProfile(repo, pii, clock.Fixed{T: now}, limiter, deny, testAccessTTL), repo, pii, limiter, deny
}

func TestGetProfile(t *testing.T) {
	ctx := context.Background()

	t.Run("without nik: returns real email, nik stays empty", func(t *testing.T) {
		u, repo, pii := newProfile(t)
		repo.EXPECT().GetByID(gomock.Any(), "acc-1").Return(entity.Account{ID: "acc-1", Status: entity.AccountActive, EmailMasked: "a***@b.id"}, nil)
		repo.EXPECT().GetEmailEncByID(gomock.Any(), "acc-1").Return([]byte("enc-email"), nil)
		pii.EXPECT().UnsealEmail([]byte("enc-email")).Return("arif@example.test", nil)

		got, err := u.GetProfile(ctx, "acc-1")
		require.NoError(t, err)
		assert.Equal(t, "acc-1", got.AccountID)
		assert.Equal(t, "arif@example.test", got.Email)
		assert.Equal(t, "a***@b.id", got.EmailMasked)
		assert.Empty(t, got.NIK, "akun tanpa NIK tidak boleh memanggil GetNIKEncByID/UnsealNIK")
		assert.False(t, got.HasNIK)
	})

	t.Run("with nik: returns real email and real nik", func(t *testing.T) {
		u, repo, pii := newProfile(t)
		repo.EXPECT().GetByID(gomock.Any(), "acc-1").Return(entity.Account{ID: "acc-1", Status: entity.AccountActive, EmailMasked: "a***@b.id", NIKMasked: "3174********0001"}, nil)
		repo.EXPECT().GetEmailEncByID(gomock.Any(), "acc-1").Return([]byte("enc-email"), nil)
		pii.EXPECT().UnsealEmail([]byte("enc-email")).Return("arif@example.test", nil)
		repo.EXPECT().GetNIKEncByID(gomock.Any(), "acc-1").Return([]byte("enc-nik"), nil)
		pii.EXPECT().UnsealNIK([]byte("enc-nik")).Return("3174010101900001", nil)

		got, err := u.GetProfile(ctx, "acc-1")
		require.NoError(t, err)
		assert.Equal(t, "arif@example.test", got.Email)
		assert.Equal(t, "3174010101900001", got.NIK)
		assert.Equal(t, "3174********0001", got.NIKMasked)
		assert.True(t, got.HasNIK)
	})

	t.Run("has nik but nik_enc NULL: nik stays empty, masked value kept", func(t *testing.T) {
		u, repo, pii := newProfile(t)
		repo.EXPECT().GetByID(gomock.Any(), "acc-1").Return(entity.Account{ID: "acc-1", Status: entity.AccountActive, EmailMasked: "a***@b.id", NIKMasked: "3174********0001"}, nil)
		repo.EXPECT().GetEmailEncByID(gomock.Any(), "acc-1").Return([]byte("enc-email"), nil)
		pii.EXPECT().UnsealEmail([]byte("enc-email")).Return("arif@example.test", nil)
		repo.EXPECT().GetNIKEncByID(gomock.Any(), "acc-1").Return(nil, nil)

		got, err := u.GetProfile(ctx, "acc-1")
		require.NoError(t, err)
		assert.Equal(t, "arif@example.test", got.Email)
		assert.Empty(t, got.NIK, "nik_enc NULL (data lama) tidak boleh di-unseal")
		assert.Equal(t, "3174********0001", got.NIKMasked)
		assert.True(t, got.HasNIK)
	})

	t.Run("disabled account: refused before any unseal", func(t *testing.T) {
		u, repo, _ := newProfile(t)
		repo.EXPECT().GetByID(gomock.Any(), "acc-1").Return(entity.Account{ID: "acc-1", Status: entity.AccountDisabled, EmailMasked: "a***@b.id", NIKMasked: "3174********0001"}, nil)

		_, err := u.GetProfile(ctx, "acc-1")
		assert.Equal(t, inbound.CodeAccountDisabled, code(err))
	})

	t.Run("account not found", func(t *testing.T) {
		u, repo, _ := newProfile(t)
		repo.EXPECT().GetByID(gomock.Any(), "gone").Return(entity.Account{}, entity.ErrNotFound)
		_, err := u.GetProfile(ctx, "gone")
		assert.Equal(t, inbound.CodeUnauthorized, code(err))
	})

	t.Run("fetch email enc error", func(t *testing.T) {
		u, repo, _ := newProfile(t)
		repo.EXPECT().GetByID(gomock.Any(), "acc-1").Return(entity.Account{ID: "acc-1", Status: entity.AccountActive}, nil)
		repo.EXPECT().GetEmailEncByID(gomock.Any(), "acc-1").Return(nil, errors.New("db"))
		_, err := u.GetProfile(ctx, "acc-1")
		assert.Equal(t, apperror.CodeInternal, code(err))
	})

	t.Run("account disappears between GetByID and GetEmailEncByID", func(t *testing.T) {
		u, repo, _ := newProfile(t)
		repo.EXPECT().GetByID(gomock.Any(), "acc-1").Return(entity.Account{ID: "acc-1", Status: entity.AccountActive}, nil)
		repo.EXPECT().GetEmailEncByID(gomock.Any(), "acc-1").Return(nil, entity.ErrNotFound)
		_, err := u.GetProfile(ctx, "acc-1")
		assert.Equal(t, inbound.CodeUnauthorized, code(err), "ErrNotFound harus dipetakan sama seperti GetByID, bukan 500")
	})

	t.Run("unseal email error", func(t *testing.T) {
		u, repo, pii := newProfile(t)
		repo.EXPECT().GetByID(gomock.Any(), "acc-1").Return(entity.Account{ID: "acc-1", Status: entity.AccountActive}, nil)
		repo.EXPECT().GetEmailEncByID(gomock.Any(), "acc-1").Return([]byte("enc-email"), nil)
		pii.EXPECT().UnsealEmail([]byte("enc-email")).Return("", errors.New("decrypt"))
		_, err := u.GetProfile(ctx, "acc-1")
		assert.Equal(t, apperror.CodeInternal, code(err))
	})

	t.Run("fetch nik enc error", func(t *testing.T) {
		u, repo, pii := newProfile(t)
		repo.EXPECT().GetByID(gomock.Any(), "acc-1").Return(entity.Account{ID: "acc-1", Status: entity.AccountActive, NIKMasked: "3174********0001"}, nil)
		repo.EXPECT().GetEmailEncByID(gomock.Any(), "acc-1").Return([]byte("enc-email"), nil)
		pii.EXPECT().UnsealEmail([]byte("enc-email")).Return("arif@example.test", nil)
		repo.EXPECT().GetNIKEncByID(gomock.Any(), "acc-1").Return(nil, errors.New("db"))
		_, err := u.GetProfile(ctx, "acc-1")
		assert.Equal(t, apperror.CodeInternal, code(err))
	})

	t.Run("account disappears between GetByID and GetNIKEncByID", func(t *testing.T) {
		u, repo, pii := newProfile(t)
		repo.EXPECT().GetByID(gomock.Any(), "acc-1").Return(entity.Account{ID: "acc-1", Status: entity.AccountActive, NIKMasked: "3174********0001"}, nil)
		repo.EXPECT().GetEmailEncByID(gomock.Any(), "acc-1").Return([]byte("enc-email"), nil)
		pii.EXPECT().UnsealEmail([]byte("enc-email")).Return("arif@example.test", nil)
		repo.EXPECT().GetNIKEncByID(gomock.Any(), "acc-1").Return(nil, entity.ErrNotFound)
		_, err := u.GetProfile(ctx, "acc-1")
		assert.Equal(t, inbound.CodeUnauthorized, code(err), "ErrNotFound harus dipetakan sama seperti GetByID, bukan 500")
	})

	t.Run("unseal nik error", func(t *testing.T) {
		u, repo, pii := newProfile(t)
		repo.EXPECT().GetByID(gomock.Any(), "acc-1").Return(entity.Account{ID: "acc-1", Status: entity.AccountActive, NIKMasked: "3174********0001"}, nil)
		repo.EXPECT().GetEmailEncByID(gomock.Any(), "acc-1").Return([]byte("enc-email"), nil)
		pii.EXPECT().UnsealEmail([]byte("enc-email")).Return("arif@example.test", nil)
		repo.EXPECT().GetNIKEncByID(gomock.Any(), "acc-1").Return([]byte("enc-nik"), nil)
		pii.EXPECT().UnsealNIK([]byte("enc-nik")).Return("", errors.New("decrypt"))
		_, err := u.GetProfile(ctx, "acc-1")
		assert.Equal(t, apperror.CodeInternal, code(err))
	})
}

// expectNIKSeal covers the NIK sealing that UpdateProfile does before it looks at the account.
func expectNIKSeal(p *mock_outbound.MockIPIIProtector) {
	p.EXPECT().SealNIK("3174010101900001").Return([]byte("enc"), nil)
	p.EXPECT().HashNIK("3174010101900001").Return([]byte("nh"))
}

func TestUpdateProfile(t *testing.T) {
	ctx := context.Background()

	t.Run("name and nik", func(t *testing.T) {
		u, repo, pii, limiter, _ := newProfileDeps(t)
		expectNIKSeal(pii)
		repo.EXPECT().GetByID(gomock.Any(), "acc-1").Return(entity.Account{ID: "acc-1", Status: entity.AccountActive}, nil)
		limiter.EXPECT().Allow(gomock.Any(), "profile:nik:acct:acc-1", 3, 24*time.Hour).Return(ticket("rl:nik"), nil)
		repo.EXPECT().UpdateProfile(gomock.Any(), "acc-1", entity.ProfileUpdate{FullName: ptr("Arif Nur"), NIKHash: []byte("nh"), NIKEnc: []byte("enc"), NIKMasked: "3174********0001"}).
			Return(entity.Account{ID: "acc-1", FullName: "Arif Nur", NIKMasked: "3174********0001"}, nil)
		got, err := u.UpdateProfile(ctx, "acc-1", "sid-1", dto.UpdateProfileRequest{FullName: ptr("  Arif Nur "), NIK: ptr("3174010101900001")})
		require.NoError(t, err)
		assert.True(t, got.HasNIK)
	})

	t.Run("NIK change revokes the current access token for AccessTTL+Leeway", func(t *testing.T) {
		u, repo, pii, limiter, deny := newProfileDeps(t)
		expectNIKSeal(pii)
		repo.EXPECT().GetByID(gomock.Any(), "acc-1").Return(entity.Account{ID: "acc-1", Status: entity.AccountActive}, nil)
		limiter.EXPECT().Allow(gomock.Any(), "profile:nik:acct:acc-1", 3, 24*time.Hour).Return(ticket("rl:nik"), nil)
		repo.EXPECT().UpdateProfile(gomock.Any(), "acc-1", gomock.Any()).Return(entity.Account{ID: "acc-1", NIKMasked: "3174********0001"}, nil)

		_, err := u.UpdateProfile(ctx, "acc-1", "sid-1", dto.UpdateProfileRequest{NIK: ptr("3174010101900001")})
		require.NoError(t, err)
		assert.Equal(t, []string{"sid-1"}, deny.sids)
		assert.Equal(t, []time.Duration{testAccessTTL + revocation.Leeway}, deny.ttls)
	})

	t.Run("4th NIK change in 24h: rate limited before the repo update", func(t *testing.T) {
		u, repo, pii, limiter, deny := newProfileDeps(t)
		expectNIKSeal(pii)
		repo.EXPECT().GetByID(gomock.Any(), "acc-1").Return(entity.Account{ID: "acc-1", Status: entity.AccountActive}, nil)
		limiter.EXPECT().Allow(gomock.Any(), "profile:nik:acct:acc-1", 3, 24*time.Hour).Return(denied("rl:nik"), nil)

		_, err := u.UpdateProfile(ctx, "acc-1", "sid-1", dto.UpdateProfileRequest{NIK: ptr("3174010101900001")})
		assert.Equal(t, apperror.CodeRateLimited, code(err))
		assert.Empty(t, deny.sids, "tanpa perubahan NIK tidak ada sesi yang dicabut")
	})

	t.Run("limiter error: fails closed with rate limited, repo not called", func(t *testing.T) {
		u, repo, pii, limiter, _ := newProfileDeps(t)
		expectNIKSeal(pii)
		repo.EXPECT().GetByID(gomock.Any(), "acc-1").Return(entity.Account{ID: "acc-1", Status: entity.AccountActive}, nil)
		limiter.EXPECT().Allow(gomock.Any(), "profile:nik:acct:acc-1", 3, 24*time.Hour).Return(outbound.RateTicket{}, errors.New("valkey down"))

		_, err := u.UpdateProfile(ctx, "acc-1", "sid-1", dto.UpdateProfileRequest{NIK: ptr("3174010101900001")})
		assert.Equal(t, apperror.CodeRateLimited, code(err))
	})

	t.Run("name only: no limiter and no denylist", func(t *testing.T) {
		u, repo, _, _, deny := newProfileDeps(t)
		repo.EXPECT().GetByID(gomock.Any(), "acc-1").Return(entity.Account{ID: "acc-1", Status: entity.AccountActive}, nil)
		repo.EXPECT().UpdateProfile(gomock.Any(), "acc-1", entity.ProfileUpdate{FullName: ptr("Arif Nur")}).Return(entity.Account{ID: "acc-1", FullName: "Arif Nur"}, nil)

		_, err := u.UpdateProfile(ctx, "acc-1", "sid-1", dto.UpdateProfileRequest{FullName: ptr("Arif Nur")})
		require.NoError(t, err)
		assert.Empty(t, deny.sids)
	})

	t.Run("revoke failure after the NIK is saved: internal error", func(t *testing.T) {
		u, repo, pii, limiter, deny := newProfileDeps(t)
		expectNIKSeal(pii)
		repo.EXPECT().GetByID(gomock.Any(), "acc-1").Return(entity.Account{ID: "acc-1", Status: entity.AccountActive}, nil)
		limiter.EXPECT().Allow(gomock.Any(), "profile:nik:acct:acc-1", 3, 24*time.Hour).Return(ticket("rl:nik"), nil)
		repo.EXPECT().UpdateProfile(gomock.Any(), "acc-1", gomock.Any()).Return(entity.Account{ID: "acc-1", NIKMasked: "3174********0001"}, nil)
		deny.err = errors.New("valkey down")

		_, err := u.UpdateProfile(ctx, "acc-1", "sid-1", dto.UpdateProfileRequest{NIK: ptr("3174010101900001")})
		assert.Equal(t, apperror.CodeInternal, code(err))
	})

	t.Run("disabled account: refused before the repo update", func(t *testing.T) {
		u, repo, _ := newProfile(t)
		repo.EXPECT().GetByID(gomock.Any(), "acc-1").Return(entity.Account{ID: "acc-1", Status: entity.AccountDisabled}, nil)

		_, err := u.UpdateProfile(ctx, "acc-1", "sid-1", dto.UpdateProfileRequest{FullName: ptr("Arif Nur")})
		assert.Equal(t, inbound.CodeAccountDisabled, code(err))
	})

	tests := []struct {
		name  string
		req   dto.UpdateProfileRequest
		setup func(*mock_outbound.MockIAccountRepository, *mock_outbound.MockIPIIProtector, *mock_outbound.MockIRateLimiter)
		want  apperror.Code
	}{
		{"nothing", dto.UpdateProfileRequest{}, nil, apperror.CodeValidationFailed},
		{"blank name", dto.UpdateProfileRequest{FullName: ptr("   ")}, nil, apperror.CodeValidationFailed},
		{"bad nik", dto.UpdateProfileRequest{NIK: ptr("123")}, nil, apperror.CodeValidationFailed},
		{"nik fails pkg/nik kab=00", dto.UpdateProfileRequest{NIK: ptr("3100010101900001")}, nil, apperror.CodeValidationFailed},
		{"seal error", dto.UpdateProfileRequest{NIK: ptr("3174010101900001")}, func(_ *mock_outbound.MockIAccountRepository, p *mock_outbound.MockIPIIProtector, _ *mock_outbound.MockIRateLimiter) {
			p.EXPECT().SealNIK(gomock.Any()).Return(nil, errors.New("aes"))
		}, apperror.CodeInternal},
		{"account gone", dto.UpdateProfileRequest{FullName: ptr("A")}, func(r *mock_outbound.MockIAccountRepository, _ *mock_outbound.MockIPIIProtector, _ *mock_outbound.MockIRateLimiter) {
			r.EXPECT().GetByID(gomock.Any(), gomock.Any()).Return(entity.Account{}, entity.ErrNotFound)
		}, inbound.CodeUnauthorized},
		{"nik taken", dto.UpdateProfileRequest{NIK: ptr("3174010101900001")}, func(r *mock_outbound.MockIAccountRepository, p *mock_outbound.MockIPIIProtector, l *mock_outbound.MockIRateLimiter) {
			p.EXPECT().SealNIK(gomock.Any()).Return([]byte("e"), nil)
			p.EXPECT().HashNIK(gomock.Any()).Return([]byte("h"))
			r.EXPECT().GetByID(gomock.Any(), gomock.Any()).Return(entity.Account{ID: "acc-1", Status: entity.AccountActive}, nil)
			l.EXPECT().Allow(gomock.Any(), "profile:nik:acct:acc-1", 3, 24*time.Hour).Return(ticket("rl:nik"), nil)
			r.EXPECT().UpdateProfile(gomock.Any(), gomock.Any(), gomock.Any()).Return(entity.Account{}, entity.ErrNIKTaken)
		}, inbound.CodeNIKAlreadyLinked},
		{"db error", dto.UpdateProfileRequest{FullName: ptr("A")}, func(r *mock_outbound.MockIAccountRepository, _ *mock_outbound.MockIPIIProtector, _ *mock_outbound.MockIRateLimiter) {
			r.EXPECT().GetByID(gomock.Any(), gomock.Any()).Return(entity.Account{ID: "acc-1", Status: entity.AccountActive}, nil)
			r.EXPECT().UpdateProfile(gomock.Any(), gomock.Any(), gomock.Any()).Return(entity.Account{}, errors.New("db"))
		}, apperror.CodeInternal},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			u, repo, pii, limiter, _ := newProfileDeps(t)
			if tt.setup != nil {
				tt.setup(repo, pii, limiter)
			}
			_, err := u.UpdateProfile(ctx, "acc-1", "sid-1", tt.req)
			assert.Equal(t, tt.want, code(err))
		})
	}
}
