package usecase

import (
	"context"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/apperror"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/clock"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/nik"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/user/internal/user/domain/dto"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/user/internal/user/domain/entity"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/user/internal/user/domain/mapper"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/user/internal/user/domain/valueobject"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/user/internal/user/port/inbound"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/user/internal/user/port/outbound"
)

// Profile implements inbound.IProfileUsecase.
type Profile struct {
	accounts outbound.IAccountRepository
	pii      outbound.IPIIProtector
	clock    clock.IClock
}

// NewProfile accepts interfaces and returns the concrete usecase.
func NewProfile(accounts outbound.IAccountRepository, pii outbound.IPIIProtector, clk clock.IClock) *Profile {
	return &Profile{accounts: accounts, pii: pii, clock: clk}
}

var _ inbound.IProfileUsecase = (*Profile)(nil)

var errNameLength = errors.New("nama 1–100 karakter")

// GetProfile returns the profile of accountID.
func (u *Profile) GetProfile(ctx context.Context, accountID string) (dto.ProfileResponse, error) {
	acc, err := u.accounts.GetByID(ctx, accountID)
	if err != nil {
		return dto.ProfileResponse{}, mapAccountErr(err)
	}
	return mapper.ToProfileResponse(acc), nil
}

// UpdateProfile changes the full name and/or the stored NIK. NIK disimpan terenkripsi dan unik antar akun.
func (u *Profile) UpdateProfile(ctx context.Context, accountID string, req dto.UpdateProfileRequest) (dto.ProfileResponse, error) {
	var upd entity.ProfileUpdate
	if req.FullName != nil {
		name := strings.TrimSpace(*req.FullName)
		if n := utf8.RuneCountInString(name); n < 1 || n > 100 {
			return dto.ProfileResponse{}, inbound.Validation("fullName", errNameLength)
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
	acc, err := u.accounts.UpdateProfile(ctx, accountID, upd)
	if err != nil {
		return dto.ProfileResponse{}, mapAccountErr(err)
	}
	return mapper.ToProfileResponse(acc), nil
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
