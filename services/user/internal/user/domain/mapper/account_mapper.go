package mapper

import (
	"time"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/user/internal/user/domain/dto"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/user/internal/user/domain/entity"
)

// ToProfileResponse maps an account to its public profile.
func ToProfileResponse(a entity.Account) dto.ProfileResponse {
	return dto.ProfileResponse{AccountID: a.ID, EmailMasked: a.EmailMasked, FullName: a.FullName, NIKMasked: a.NIKMasked, HasNIK: a.HasNIK()}
}

// ToTokenResponse assembles the login/refresh response.
func ToTokenResponse(a entity.Account, access string, accessExp time.Time, refresh string, refreshExp time.Time) dto.TokenResponse {
	return dto.TokenResponse{
		TokenType:             "Bearer",
		AccessToken:           access,
		AccessTokenExpiresAt:  accessExp.UTC().Format(time.RFC3339),
		RefreshToken:          refresh,
		RefreshTokenExpiresAt: refreshExp.UTC().Format(time.RFC3339),
		Account:               ToProfileResponse(a),
	}
}
