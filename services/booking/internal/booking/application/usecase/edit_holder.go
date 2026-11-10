package usecase

import (
	pkgnik "github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/nik"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/dto"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/entity"
	vo "github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/valueobject"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/inbound"
)

// holder is the resolved identity of one ticket line: either freshly validated input or copied
// verbatim from a live (HELD) item of the same booking (keepFromItemId).
type holder struct {
	hash   []byte
	enc    []byte
	masked string
	name   string
}

// liveItems indexes the HELD items of a booking by id: the only items a keepFromItemId may point at.
func liveItems(items []entity.BookingItem) map[string]entity.BookingItem {
	live := make(map[string]entity.BookingItem, len(items))
	for _, it := range items {
		if it.Status == vo.ItemHeld {
			live[it.ID] = it
		}
	}
	return live
}

// resolveHolder turns an attendee input into a holder. A kept holder copies the ciphertext and hash
// without decrypting, so no plaintext NIK exists in this flow (SRS-Q-SEC-09). Every "not usable"
// reason (unknown, other booking, not HELD, not a UUID) yields one identical error so ids cannot be probed.
func (uc *Edit) resolveHolder(a dto.AttendeeInput, live map[string]entity.BookingItem) (holder, error) {
	if a.KeepFromItemID != "" {
		if a.NIK != "" || a.FullName != "" {
			return holder{}, inbound.Validation("attendee", "keepFromItemId tidak boleh digabung dengan nik atau fullName")
		}
		src, ok := live[a.KeepFromItemID]
		if !isUUID(a.KeepFromItemID) || !ok {
			return holder{}, inbound.Validation("keepFromItemId", "keepFromItemId tidak valid")
		}
		return holder{hash: src.AttendeeNIKHash, enc: src.AttendeeNIKEnc, masked: src.AttendeeNIKMasked, name: src.AttendeeName}, nil
	}
	if a.NIK == "" && a.FullName == "" {
		return holder{}, inbound.Validation("attendee", "attendee wajib berisi nik dan fullName, atau keepFromItemId")
	}
	if len(a.FullName) < 2 || len(a.FullName) > 100 {
		return holder{}, inbound.Validation("fullName", "fullName harus 2..100 karakter")
	}
	if err := pkgnik.Validate(a.NIK, uc.clock.Now()); err != nil {
		return holder{}, inbound.ErrNIKInvalid
	}
	nik, err := vo.ParseNIK(a.NIK, uc.nik)
	if err != nil {
		return holder{}, inbound.ErrNIKInvalid
	}
	return holder{hash: nik.Hash(), enc: nik.Enc(), masked: nik.Masked(), name: a.FullName}, nil
}
