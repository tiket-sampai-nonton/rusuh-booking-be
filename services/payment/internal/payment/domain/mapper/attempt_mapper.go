package mapper

import (
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/payment/internal/payment/domain/dto"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/payment/internal/payment/domain/entity"
)

// AttemptToDTO converts an attempt to the API view. A zero ExpiresAt is omitted.
func AttemptToDTO(a entity.Attempt) dto.Attempt {
	out := dto.Attempt{
		AttemptNo: a.AttemptNo, Method: string(a.Method), Status: string(a.Status), BookingVersion: a.BookingVersion,
	}
	if !a.ExpiresAt.IsZero() {
		t := a.ExpiresAt.UTC()
		out.ExpiresAt = &t
	}
	if a.Instruction != nil {
		out.RedirectURL, out.SuccessReturnURL, out.FailedReturnURL = a.Instruction.RedirectURL, a.Instruction.SuccessReturnURL, a.Instruction.FailedReturnURL
	}
	return out
}

// AttemptPtrToDTO converts an optional attempt; nil stays nil (JSON null).
func AttemptPtrToDTO(a *entity.Attempt) *dto.Attempt {
	if a == nil {
		return nil
	}
	v := AttemptToDTO(*a)
	return &v
}
