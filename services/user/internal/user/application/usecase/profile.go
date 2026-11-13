package usecase

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/apperror"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/clock"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/nik"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/revocation"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/user/internal/user/domain/dto"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/user/internal/user/domain/entity"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/user/internal/user/domain/mapper"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/user/internal/user/domain/valueobject"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/user/internal/user/port/inbound"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/user/internal/user/port/outbound"
)

// nikChangeLimit: maksimal 3 pergantian NIK per akun per 24 jam (D-U4). Membatasi enumerasi NIK lewat 409.
const (
	nikChangeLimit  = 3
	nikChangeWindow = 24 * time.Hour
)

// Profile implements inbound.IProfileUsecase.
type Profile struct {
	accounts  outbound.IAccountRepository
	pii       outbound.IPIIProtector
	clock     clock.IClock
	limiter   outbound.IRateLimiter
	denylist  revocation.Denylist
	accessTTL time.Duration // umur access token; sesi yang diganti NIK-nya dicabut selama accessTTL+Leeway
}

// NewProfile accepts interfaces and returns the concrete usecase.
func NewProfile(accounts outbound.IAccountRepository, pii outbound.IPIIProtector, clk clock.IClock,
	limiter outbound.IRateLimiter, denylist revocation.Denylist, accessTTL time.Duration) *Profile {
	return &Profile{accounts: accounts, pii: pii, clock: clk, limiter: limiter, denylist: denylist, accessTTL: accessTTL}
}

var _ inbound.IProfileUsecase = (*Profile)(nil)

var (
	errNameLength = errors.New("nama 1–100 karakter")
	errNameChars  = errors.New("nama berisi karakter tidak diizinkan")
)

// GetProfile returns the profile of accountID. Email dan NIK dikirim dalam bentuk asli di sini saja
// (pemilik akun melihat datanya sendiri); endpoint/alur lain tetap hanya mengirim bentuk tersamar.
func (u *Profile) GetProfile(ctx context.Context, accountID string) (dto.ProfileResponse, error) {
	acc, err := u.accounts.GetByID(ctx, accountID)
	if err != nil {
		return dto.ProfileResponse{}, mapAccountErr(err)
	}
	if !acc.Active() {
		return dto.ProfileResponse{}, inbound.ErrAccountDisabled
	}
	resp := mapper.ToProfileResponse(acc)

	emailEnc, err := u.accounts.GetEmailEncByID(ctx, accountID)
	if err != nil {
		return dto.ProfileResponse{}, mapAccountErr(err)
	}
	resp.Email, err = u.pii.UnsealEmail(emailEnc)
	if err != nil {
		return dto.ProfileResponse{}, apperror.Internal(err)
	}

	if acc.HasNIK() {
		nikEnc, err := u.accounts.GetNIKEncByID(ctx, accountID)
		if err != nil {
			return dto.ProfileResponse{}, mapAccountErr(err)
		}
		// Data lama bisa punya nik_masked tanpa nik_enc: NIK asli dibiarkan kosong, bentuk tersamar tetap.
		if nikEnc != nil {
			resp.NIK, err = u.pii.UnsealNIK(nikEnc)
			if err != nil {
				return dto.ProfileResponse{}, apperror.Internal(err)
			}
		}
	}

	return resp, nil
}

// UpdateProfile changes the full name and/or the stored NIK. NIK disimpan terenkripsi dan unik antar akun.
// Setiap perubahan NIK dibatasi per akun dan mencabut access token sesi yang memintanya (sessionID), karena token itu
// masih membawa nik_hash lama; klien memanggil POST /auth/refresh untuk token baru. Perubahan nama saja tidak keduanya.
func (u *Profile) UpdateProfile(ctx context.Context, accountID, sessionID string, req dto.UpdateProfileRequest) (dto.ProfileResponse, error) {
	var upd entity.ProfileUpdate
	if req.FullName != nil {
		name := strings.TrimSpace(*req.FullName)
		if n := utf8.RuneCountInString(name); n < 1 || n > 100 {
			return dto.ProfileResponse{}, inbound.Validation("fullName", errNameLength)
		}
		if strings.IndexFunc(name, isNameForbidden) >= 0 {
			return dto.ProfileResponse{}, inbound.Validation("fullName", errNameChars)
		}
		upd.FullName = &name
	}
	if req.NIK != nil {
		s := strings.TrimSpace(*req.NIK)
		// pkg/nik.Validate enforces the full Kemendagri rule set (SDD-RBT §4.9, platform R1).
		// Called in usecase, not domain, to avoid importing pkg/ from domain layer.
		if err := nik.Validate(s, u.clock.Now()); err != nil {
			return dto.ProfileResponse{}, inbound.Validation("nik", err)
		}
		v, err := valueobject.NewNIK(s)
		if err != nil {
			return dto.ProfileResponse{}, inbound.Validation("nik", err)
		}
		enc, err := u.pii.SealNIK(v.String())
		if err != nil {
			return dto.ProfileResponse{}, apperror.Internal(err)
		}
		upd.NIKHash, upd.NIKEnc, upd.NIKMasked = u.pii.HashNIK(v.String()), enc, maskNIK(v.String())
	}
	if upd.FullName == nil && upd.NIKHash == nil {
		return dto.ProfileResponse{}, inbound.ErrNothingToUpdate
	}
	current, err := u.accounts.GetByID(ctx, accountID)
	if err != nil {
		return dto.ProfileResponse{}, mapAccountErr(err)
	}
	if !current.Active() {
		return dto.ProfileResponse{}, inbound.ErrAccountDisabled
	}
	nikChanged := req.NIK != nil
	// Gagal tertutup: tanpa hitungan yang pasti, pergantian NIK tidak diizinkan.
	if nikChanged {
		ticket, err := u.limiter.Allow(ctx, "profile:nik:acct:"+accountID, nikChangeLimit, nikChangeWindow)
		if err != nil || !ticket.Allowed {
			return dto.ProfileResponse{}, inbound.ErrRateLimited
		}
	}
	acc, err := u.accounts.UpdateProfile(ctx, accountID, upd)
	if err != nil {
		return dto.ProfileResponse{}, mapAccountErr(err)
	}
	if nikChanged {
		// NIK sudah tersimpan di sini. Bila pencabutan gagal, klien mengulang: percobaan ulang memakai satu hitungan limiter
		// lagi, dan NIK yang sama untuk akun yang sama tidak menghasilkan 409 (uq_accounts_nik hanya untuk akun lain).
		if err := u.denylist.Revoke(ctx, sessionID, u.accessTTL+revocation.Leeway); err != nil {
			return dto.ProfileResponse{}, apperror.Internal(err)
		}
	}
	return mapper.ToProfileResponse(acc), nil
}

// isNameForbidden: kontrol dan bidi override (mis. U+202E) bisa menyamarkan nama di UI dan log.
func isNameForbidden(r rune) bool {
	return unicode.IsControl(r) || unicode.Is(unicode.Bidi_Control, r)
}

func mapAccountErr(err error) error {
	switch {
	case errors.Is(err, entity.ErrNotFound):
		return inbound.ErrUnauthorized
	case errors.Is(err, entity.ErrNIKTaken):
		return inbound.ErrNIKAlreadyLinked
	default:
		return apperror.Internal(err)
	}
}
