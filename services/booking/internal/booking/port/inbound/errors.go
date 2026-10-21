// Package entity-adjacent errors for the Booking domain. The error catalog is defined here so every
// layer (usecase, adapter, handler) shares one source of truth for errorCode + HTTP status (design §11).
package inbound

import (
	"net/http"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/apperror"
)

// Error codes of the Booking domain (design §11 / tasks Task 1). These are part of the API contract:
// usecases return them, the HTTP adapter translates them to status + envelope.
const (
	CodeValidation              apperror.Code = "VALIDATION_ERROR"
	CodeNotFound                apperror.Code = "NOT_FOUND"
	CodeUnauthorized            apperror.Code = "UNAUTHORIZED"
	CodeAdmissionToken          apperror.Code = "ADMISSION_TOKEN_INVALID"
	CodeVisitorMismatch         apperror.Code = "VISITOR_MISMATCH"
	CodeNIKInvalid              apperror.Code = "NIK_INVALID"
	CodeQuantityExceeds         apperror.Code = "QUANTITY_EXCEEDS_LIMIT"
	CodeMixedNotAllowed         apperror.Code = "MIXED_CATEGORIES_NOT_ALLOWED"
	CodeCategoryUnavailable     apperror.Code = "CATEGORY_UNAVAILABLE"
	CodeBuyerQuota              apperror.Code = "BUYER_QUOTA_EXHAUSTED"
	CodeNikRegistered           apperror.Code = "ATTENDEE_NIK_ALREADY_REGISTERED"
	CodeNikDuplicate            apperror.Code = "ATTENDEE_NIK_DUPLICATE_IN_ORDER"
	CodeIdemReused              apperror.Code = "IDEMPOTENCY_KEY_REUSED"
	CodeBookingLocked           apperror.Code = "BOOKING_LOCKED"
	CodeVersionConflict         apperror.Code = "BOOKING_VERSION_CONFLICT"
	CodeEditLimit               apperror.Code = "EDIT_LIMIT_REACHED"
	CodeNotPayable              apperror.Code = "BOOKING_NOT_PAYABLE"
	CodePaymentExpired          apperror.Code = "PAYMENT_EXPIRED"
	CodeSessionExpired          apperror.Code = "SESSION_EXPIRED"
	CodeTurnMissed              apperror.Code = "TURN_MISSED"
	CodeRateLimited             apperror.Code = "RATE_LIMITED"
	CodeDependency              apperror.Code = "DEPENDENCY_UNAVAILABLE"
	CodeDeadlineAlreadyExtended apperror.Code = "DEADLINE_ALREADY_EXTENDED"
	CodeRulesLocked             apperror.Code = "EVENT_RULES_LOCKED"
	CodeSupplyMismatch          apperror.Code = "SUPPLY_CATEGORY_MISMATCH"
)

// Domain errors (design §11). Each carries the exact errorCode + HTTP status.
var (
	ErrValidation              = apperror.New(CodeValidation, http.StatusBadRequest, "permintaan tidak valid")
	ErrNotFound                = apperror.New(CodeNotFound, http.StatusNotFound, "data tidak ditemukan")
	ErrUnauthorized            = apperror.New(CodeUnauthorized, http.StatusUnauthorized, "akses token tidak valid atau kedaluwarsa")
	ErrAdmissionToken          = apperror.New(CodeAdmissionToken, http.StatusForbidden, "admission token tidak valid atau sudah dipakai")
	ErrVisitorMismatch         = apperror.New(CodeVisitorMismatch, http.StatusForbidden, "sesi milik pengunjung lain")
	ErrNIKInvalid              = apperror.New(CodeNIKInvalid, http.StatusUnprocessableEntity, "struktur NIK tidak valid")
	ErrQuantityExceeds         = apperror.New(CodeQuantityExceeds, http.StatusUnprocessableEntity, "jumlah tiket melebihi batas per pesanan")
	ErrMixedNotAllowed         = apperror.New(CodeMixedNotAllowed, http.StatusUnprocessableEntity, "kategori campuran tidak diizinkan")
	ErrCategoryUnavailable     = apperror.New(CodeCategoryUnavailable, http.StatusConflict, "kategori tidak tersedia")
	ErrBuyerQuota              = apperror.New(CodeBuyerQuota, http.StatusConflict, "kuota pembeli untuk NIK ini sudah habis")
	ErrNikRegistered           = apperror.New(CodeNikRegistered, http.StatusConflict, "NIK pemegang sudah terdaftar pada event ini")
	ErrNikDuplicate            = apperror.New(CodeNikDuplicate, http.StatusConflict, "NIK pemegang duplikat dalam satu pesanan")
	ErrIdemReused              = apperror.New(CodeIdemReused, http.StatusConflict, "Idempotency-Key dipakai ulang dengan isi berbeda")
	ErrBookingLocked           = apperror.New(CodeBookingLocked, http.StatusConflict, "pesanan terkunci; pembayaran sudah dimulai")
	ErrVersionConflict         = apperror.New(CodeVersionConflict, http.StatusConflict, "versi pesanan tidak cocok")
	ErrEditLimit               = apperror.New(CodeEditLimit, http.StatusConflict, "batas ubah pesanan tercapai")
	ErrNotPayable              = apperror.New(CodeNotPayable, http.StatusConflict, "tiket belum dapat diterbitkan")
	ErrPaymentExpired          = apperror.New(CodePaymentExpired, http.StatusGone, "waktu pembayaran sudah habis")
	ErrSessionExpired          = apperror.New(CodeSessionExpired, http.StatusGone, "sesi sudah kedaluwarsa")
	ErrTurnMissed              = apperror.New(CodeTurnMissed, http.StatusGone, "giliran terlewat")
	ErrRateLimited             = apperror.New(CodeRateLimited, http.StatusTooManyRequests, "terlalu banyak permintaan; coba lagi nanti")
	ErrDependency              = apperror.New(CodeDependency, http.StatusServiceUnavailable, "dependency sementara tidak tersedia")
	ErrDeadlineAlreadyExtended = apperror.New(CodeDeadlineAlreadyExtended, http.StatusConflict, "batas waktu sudah pernah diperpanjang")
	ErrRulesLocked             = apperror.New(CodeRulesLocked, http.StatusConflict, "aturan event sudah terkunci")
	ErrSupplyMismatch          = apperror.New(CodeSupplyMismatch, http.StatusConflict, "total kapasitas tidak cocok dengan jumlah kategori")
)

// Validation returns a VALIDATION_ERROR (400) with the failing field in details.
func Validation(field, msg string) *apperror.Error {
	return apperror.New(CodeValidation, http.StatusBadRequest, msg).WithDetails(map[string]string{field: msg})
}
