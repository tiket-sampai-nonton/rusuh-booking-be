package entity

import "errors"

// Domain sentinel errors (design §9). Each is mapped to an apperror code + HTTP status in
// infrastructure/http; usecases return these so handlers stay thin and never leak internals.
var (
	// ErrAccountInQueue: the bearer's account already has an active entry that this device does not hold.
	ErrAccountInQueue = errors.New("queue: akun sudah berada di antrean")

	ErrNotOnSale  = errors.New("queue: event tidak dalam masa penjualan")
	ErrSoldOut    = errors.New("queue: tiket sudah habis")
	ErrTurnMissed = errors.New("queue: giliran terlewat")
	// ErrNotAdmitted: claim for an entry that is still WAITING (it has no admitted turn yet).
	ErrNotAdmitted = errors.New("queue: entry belum mendapat giliran")

	ErrNotFound = errors.New("queue: entry tidak ditemukan")
	// ErrVisitorTokenInvalid: bad signature, expired, rotated out or device mismatch (R01.6) -> 401.
	ErrVisitorTokenInvalid = errors.New("queue: visitor token tidak valid")
	// ErrEntryNotFound: a valid token whose entry ended (heartbeat) -> 404 ENTRY_NOT_FOUND.
	ErrEntryNotFound = errors.New("queue: entry sudah berakhir atau tidak ada")
	ErrEventNotFound = errors.New("queue: event tidak ditemukan")
	ErrNotOwner      = errors.New("queue: entry bukan milik visitor ini")
	ErrEntryMoved    = errors.New("queue: entry dipindahkan ke perangkat lain")

	ErrDependencyUnavailable = errors.New("queue: dependency tidak tersedia")
)
