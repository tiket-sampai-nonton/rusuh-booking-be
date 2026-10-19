package inbound

import (
	"context"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/user/internal/user/domain/dto"
)

//go:generate go tool mockgen -source=auth_usecase.go -destination=../../../../_mock/user/port/inbound/auth_usecase_mock.go -package=mock_inbound

// ClientInfo describes the caller for rate limiting and audit.
type ClientInfo struct {
	IP        string
	UserAgent string
}

// IAuthUsecase covers passwordless OTP login and session rotation (ADR-020).
type IAuthUsecase interface {
	RequestOTP(ctx context.Context, req dto.RequestOTPRequest, client ClientInfo) (dto.RequestOTPResponse, error)
	VerifyOTP(ctx context.Context, req dto.VerifyOTPRequest, client ClientInfo) (dto.TokenResponse, error)
	Refresh(ctx context.Context, req dto.RefreshRequest, client ClientInfo) (dto.TokenResponse, error)
	Logout(ctx context.Context, req dto.RefreshRequest, client ClientInfo) error
}

// IProfileUsecase reads and updates the logged-in buyer's profile.
type IProfileUsecase interface {
	GetProfile(ctx context.Context, accountID string) (dto.ProfileResponse, error)
	UpdateProfile(ctx context.Context, accountID string, req dto.UpdateProfileRequest) (dto.ProfileResponse, error)
}

// IAccessTokenVerifier validates the Bearer token and returns the account id.
type IAccessTokenVerifier interface {
	VerifyAccess(token string) (accountID string, err error)
}
