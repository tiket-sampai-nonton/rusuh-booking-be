package usecase

import (
	"context"
	"net/url"
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
	store          outbound.IQueueStore
	tokens         outbound.ITokenIssuer
	clock          clock.IClock
	bookingPageURL string // public booking page base (BOOKING_PAGE_BASE_URL), e.g. "https://app/book"
}

// NewClaimTurn accepts interfaces and returns the concrete usecase. bookingPageBase is the public booking page base
// URL; it is joined with the (path-escaped) event id to form the page URL. The URL never carries a token.
func NewClaimTurn(store outbound.IQueueStore, tokens outbound.ITokenIssuer, clk clock.IClock, bookingPageBase string) *ClaimTurn {
	return &ClaimTurn{store: store, tokens: tokens, clock: clk, bookingPageURL: strings.TrimRight(bookingPageBase, "/")}
}

// Claim validates the turn via claim.lua then issues an EdDSA admission token (TTL 60s). The allocation id comes
// from claim.lua's own reply (design §5.3), never from a second GetEntry: a second read would race a concurrent
// change and could sign a token for a stale allocation.
func (u *ClaimTurn) Claim(ctx context.Context, in inbound.ClaimInput) (dto.ClaimResponse, error) {
	if err := checkTokenGen(ctx, u.store, in.EventID, in.VisitorID, in.Gen); err != nil {
		return dto.ClaimResponse{}, err
	}
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
	// DEPRECATED (owner Q4 ii): bookingUrl kept for one release; the client navigates from AdmissionToken.
	return dto.ClaimResponse{
		AdmissionToken: token,
		BookingURL:     u.bookingPageURL + "/" + url.PathEscape(in.EventID),
	}, nil
}
