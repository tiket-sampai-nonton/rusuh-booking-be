package inbound

import (
	"net/http"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/apperror"
)

// Error codes of the User use cases (kontrak API: docs/contracts/openapi/user.yaml). Error ini bagian
// dari kontrak port inbound: usecase mengembalikannya, adapter HTTP menerjemahkannya ke status + envelope.
const (
	CodeOTPInvalid       apperror.Code = "OTP_INVALID"
	CodeOTPExpired       apperror.Code = "OTP_EXPIRED"
	CodeOTPLocked        apperror.Code = "OTP_LOCKED"
	CodeOTPResendTooSoon apperror.Code = "OTP_RESEND_TOO_SOON"
	CodeUnauthorized     apperror.Code = "UNAUTHORIZED"
	CodeSessionInvalid   apperror.Code = "SESSION_INVALID"
	CodeAccountDisabled  apperror.Code = "ACCOUNT_DISABLED"
	CodeNIKAlreadyLinked apperror.Code = "NIK_ALREADY_LINKED"
)

// Errors returned by usecases.
var (
	ErrOTPInvalid       = apperror.New(CodeOTPInvalid, http.StatusUnauthorized, "kode OTP salah")
	ErrOTPExpired       = apperror.New(CodeOTPExpired, http.StatusUnauthorized, "kode OTP kedaluwarsa atau belum diminta; minta kode baru")
	ErrOTPLocked        = apperror.New(CodeOTPLocked, http.StatusTooManyRequests, "terlalu banyak percobaan; minta kode baru")
	ErrOTPResendTooSoon = apperror.New(CodeOTPResendTooSoon, http.StatusTooManyRequests, "tunggu sebentar sebelum meminta kode lagi")
	ErrRateLimited      = apperror.New(apperror.CodeRateLimited, http.StatusTooManyRequests, "terlalu banyak permintaan; coba lagi nanti")
	ErrUnauthorized     = apperror.New(CodeUnauthorized, http.StatusUnauthorized, "akses token tidak valid atau kedaluwarsa")
	ErrSessionInvalid   = apperror.New(CodeSessionInvalid, http.StatusUnauthorized, "sesi tidak valid; silakan login ulang")
	ErrAccountDisabled  = apperror.New(CodeAccountDisabled, http.StatusForbidden, "akun dinonaktifkan")
	ErrNIKAlreadyLinked = apperror.New(CodeNIKAlreadyLinked, http.StatusConflict, "NIK ini sudah tertaut ke akun lain")
	ErrMailUnavailable  = apperror.New(apperror.CodeDependencyUnavailable, http.StatusServiceUnavailable, "email OTP belum bisa dikirim; coba lagi")
)

// ErrNothingToUpdate is returned by UpdateProfile when no field is sent.
var ErrNothingToUpdate = apperror.New(apperror.CodeValidationFailed, http.StatusBadRequest, "tidak ada field yang diubah")

// Validation returns VALIDATION_ERROR (400) with the failing field in details.
func Validation(field string, err error) error {
	return apperror.New(apperror.CodeValidationFailed, http.StatusBadRequest, err.Error()).WithDetails(map[string]string{field: err.Error()})
}
