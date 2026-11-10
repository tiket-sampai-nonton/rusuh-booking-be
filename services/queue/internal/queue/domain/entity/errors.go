package entity

import "errors"

// Domain sentinel errors (design §9). Each is mapped to an apperror code + HTTP status in
// infrastructure/http; usecases return these so handlers stay thin and never leak internals.
var (
	// ErrAccountInQueue: the bearer's account already has an active entry that this device does not hold.
	ErrAccountInQueue = errors.New("queue: akun sudah berada di antrean")

	// ErrEntryClaimed: DELETE on a CLAIMED entry. Booking owns the session once the entry is claimed, so the entry
	// is neither ended nor released here; the client must cancel the booking session instead -> 409 ENTRY_CLAIMED.
	ErrEntryClaimed = errors.New("queue: entry sudah diklaim; batalkan sesi booking")

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
	// RESERVED (owner Q5): no path produces it yet; future design SRS-F-01-10, spec Req 7.4, task 14.2.
	ErrEntryMoved = errors.New("queue: entry dipindahkan ke perangkat lain")

	ErrDependencyUnavailable = errors.New("queue: dependency tidak tersedia")

	// ErrNotConfigured: join for an event that has no queue settings row (QSA-6) -> 409 QUEUE_NOT_CONFIGURED.
	ErrNotConfigured = errors.New("queue: antrean belum dikonfigurasi untuk event ini")
	// ErrSettingsNotFound: admin GET for an event with no settings row -> 404 QUEUE_NOT_CONFIGURED.
	ErrSettingsNotFound = errors.New("queue: pengaturan antrean belum ada")
	// ErrRulesLocked: a locked rule would change while the sale is open (QSA-3) -> 409 EVENT_RULES_LOCKED.
	ErrRulesLocked = errors.New("queue: aturan event terkunci selama penjualan")
)
