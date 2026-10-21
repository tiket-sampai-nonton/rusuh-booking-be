package usecase

import (
	"context"
	"strings"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/clock"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/domain/dto"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/port/inbound"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/port/outbound"
)

// ClaimTurn implements inbound.IClaimUsecase: claims an admitted turn and issues the one-time
// admission token (design §2.4, §4.3).
type ClaimTurn struct {
	store      outbound.IQueueStore
	tokens     outbound.ITokenIssuer
	clock      clock.IClock
	bookingURL string // base URL template, e.g. "https://app/book"
}

// NewClaimTurn accepts interfaces and returns the concrete usecase. bookingURLBase is joined with the
// entry id to form the per-entry booking URL.
func NewClaimTurn(store outbound.IQueueStore, tokens outbound.ITokenIssuer, clk clock.IClock, bookingURLBase string) *ClaimTurn {
	return &ClaimTurn{store: store, tokens: tokens, clock: clk, bookingURL: strings.TrimRight(bookingURLBase, "/")}
}

// Claim validates the turn via claim.lua then issues an EdDSA admission token (TTL 60s).
func (u *ClaimTurn) Claim(ctx context.Context, in inbound.ClaimInput) (dto.ClaimResponse, error) {
	now := u.clock.Now()
	if _, err := u.store.Claim(ctx, in.EventID, in.EntryID, in.VisitorID, now.UnixMilli()); err != nil {
		return dto.ClaimResponse{}, err
	}
	// Read allocationId from Valkey — used as JTI so Booking can activate by admission primary key.
	entry, _, _ := u.store.GetEntry(ctx, in.EventID, in.EntryID)
	allocationID := entry.AllocationID // set during promote.lua

	token, _, err := u.tokens.IssueAdmission(in.EntryID, in.EventID, allocationID, now)
	if err != nil {
		return dto.ClaimResponse{}, err
	}
	return dto.ClaimResponse{
		AdmissionToken: token,
		BookingURL:     u.bookingURL + "/" + in.EntryID,
	}, nil
}
