package usecase

import (
	"context"
	"strings"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/clock"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/domain/dto"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/domain/entity"
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

// Claim validates the turn via claim.lua then issues an EdDSA admission token (TTL 60s). The allocation id comes
// from claim.lua's own reply (design §5.3), never from a second GetEntry: a second read would race a concurrent
// change and could sign a token for a stale allocation.
func (u *ClaimTurn) Claim(ctx context.Context, in inbound.ClaimInput) (dto.ClaimResponse, error) {
	now := u.clock.Now()
	res, err := u.store.Claim(ctx, in.EventID, in.EntryID, in.VisitorID, now.UnixMilli())
	if err != nil {
		return dto.ClaimResponse{}, err
	}
	if res.AllocationID == "" {
		// The entry was admitted (claim.lua only answers OK for an ADMITTED turn) but carries no allocation id. This
		// should not happen (promote.lua always writes one); refuse rather than sign an unusable token.
		return dto.ClaimResponse{}, entity.ErrDependencyUnavailable
	}

	token, _, err := u.tokens.IssueAdmission(in.EntryID, in.EventID, res.AllocationID, now)
	if err != nil {
		return dto.ClaimResponse{}, err
	}
	return dto.ClaimResponse{
		AdmissionToken: token,
		BookingURL:     u.bookingURL + "/" + in.EntryID,
	}, nil
}
