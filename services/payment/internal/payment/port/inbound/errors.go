package inbound

import (
	"net/http"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/apperror"
)

// Error codes of the Payment API (payment.yaml, design §2.10).
const (
	CodeNotPayable          apperror.Code = "BOOKING_NOT_PAYABLE"
	CodeIdemReused          apperror.Code = "IDEMPOTENCY_KEY_REUSED"
	CodeMethodUnavailable   apperror.Code = "PAYMENT_METHOD_UNAVAILABLE"
	CodeProviderUnavailable apperror.Code = "PAYMENT_PROVIDER_UNAVAILABLE"
	CodePageTokenInvalid    apperror.Code = "PAYMENT_PAGE_TOKEN_INVALID"
	CodeSignatureInvalid    apperror.Code = "WEBHOOK_SIGNATURE_INVALID"
)

// Errors shared by usecases and the HTTP adapter. Booking that is not the caller's is reported as not found
// so a booking id does not reveal who owns it (OQ-19).
var (
	ErrNotFound            = apperror.New(apperror.CodeNotFound, http.StatusNotFound, "booking tidak ditemukan")
	ErrNotPayable          = apperror.New(CodeNotPayable, http.StatusConflict, "booking tidak menunggu pembayaran")
	ErrIdemReused          = apperror.New(CodeIdemReused, http.StatusConflict, "Idempotency-Key sudah dipakai untuk permintaan lain")
	ErrMethodUnavailable   = apperror.New(CodeMethodUnavailable, http.StatusUnprocessableEntity, "metode pembayaran tidak tersedia")
	ErrProviderUnavailable = apperror.New(CodeProviderUnavailable, http.StatusServiceUnavailable, "penyedia pembayaran sedang gangguan, coba lagi")
	ErrPageTokenInvalid    = apperror.New(CodePageTokenInvalid, http.StatusForbidden, "token halaman bayar tidak valid")
	ErrSignatureInvalid    = apperror.New(CodeSignatureInvalid, http.StatusUnauthorized, "signature webhook tidak valid")
	ErrDependency          = apperror.New(apperror.CodeDependencyUnavailable, http.StatusServiceUnavailable, "layanan terkait sedang tidak tersedia")
)

// Validation returns a 400 error for one invalid field.
func Validation(field, msg string) *apperror.Error {
	return apperror.New(apperror.CodeValidationFailed, http.StatusBadRequest, msg).WithDetails(map[string]string{"field": field})
}
