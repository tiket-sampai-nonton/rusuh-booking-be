package entity

import (
	"errors"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/domain/valueobject"
)

// Domain sentinel errors (design §9). Each is mapped to an apperror code + HTTP status in
// infrastructure/http; usecases return these so handlers stay thin and never leak internals.
// ErrNikInvalid aliases the value-object error so both packages reference the same sentinel.
var (
	ErrNikInvalid    = valueobject.ErrNikInvalid
	ErrNikRequired   = errors.New("queue: NIK wajib diisi")
	ErrEmailMismatch = errors.New("queue: email dan konfirmasi email tidak sama")
	ErrConsentNik    = errors.New("queue: persetujuan penggunaan NIK wajib true")

	ErrNikInQueue     = errors.New("queue: NIK sudah berada di antrean")
	ErrVisitorInQueue = errors.New("queue: perangkat sudah berada di antrean")
	ErrEmailInQueue   = errors.New("queue: email sudah berada di antrean")
	ErrQuotaExhausted = errors.New("queue: kuota pembeli untuk NIK ini sudah habis")

	ErrNotOnSale  = errors.New("queue: event tidak dalam masa penjualan")
	ErrSoldOut    = errors.New("queue: tiket sudah habis")
	ErrTurnMissed = errors.New("queue: giliran terlewat")

	ErrNotFound   = errors.New("queue: entry tidak ditemukan")
	ErrNotOwner   = errors.New("queue: entry bukan milik visitor ini")
	ErrEntryMoved = errors.New("queue: entry dipindahkan ke perangkat lain")

	ErrDependencyUnavailable = errors.New("queue: dependency tidak tersedia")
)
