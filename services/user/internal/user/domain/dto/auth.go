package dto

// RequestOTPRequest is the body of POST /api/v1/auth/otp/request.
type RequestOTPRequest struct {
	Email string `json:"email"`
}

// RequestOTPResponse is returned with 202 whether or not the email has an account (anti enumerasi).
type RequestOTPResponse struct {
	ExpiresInSeconds   int `json:"expiresInSeconds"`
	ResendAfterSeconds int `json:"resendAfterSeconds"`
}

// VerifyOTPRequest is the body of POST /api/v1/auth/otp/verify.
type VerifyOTPRequest struct {
	Email string `json:"email"`
	Code  string `json:"code"`
}

// RefreshRequest is the body of POST /api/v1/auth/refresh and /api/v1/auth/logout.
type RefreshRequest struct {
	RefreshToken string `json:"refreshToken"`
}

// TokenResponse carries the access token (Bearer) and the rotating refresh token.
type TokenResponse struct {
	TokenType             string          `json:"tokenType"`
	AccessToken           string          `json:"accessToken"`
	AccessTokenExpiresAt  string          `json:"accessTokenExpiresAt"`
	RefreshToken          string          `json:"refreshToken"`
	RefreshTokenExpiresAt string          `json:"refreshTokenExpiresAt"`
	Account               ProfileResponse `json:"account"`
}

// ProfileResponse is returned by GET/PATCH /api/v1/me. NIK hanya dikirim tersamar.
type ProfileResponse struct {
	AccountID   string `json:"accountId"`
	EmailMasked string `json:"emailMasked"`
	FullName    string `json:"fullName,omitempty"`
	NIKMasked   string `json:"nikMasked,omitempty"`
	HasNIK      bool   `json:"hasNik"`
}

// UpdateProfileRequest is the body of PATCH /api/v1/me. Field yang tidak dikirim tidak diubah.
type UpdateProfileRequest struct {
	FullName *string `json:"fullName,omitempty"`
	NIK      *string `json:"nik,omitempty"`
}
