package usecase

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/clock"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/payment/internal/payment/domain/dto"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/payment/internal/payment/port/inbound"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/payment/internal/payment/port/outbound"
)

// CreatePageToken issues the one-time token a mobile app puts in the WebView URL, so the visitor token never
// appears in a URL (R2.1). Only the SHA-256 of the token is stored.
type CreatePageToken struct {
	booking outbound.IBookingClient
	tokens  outbound.IPageTokenStore
	clock   clock.IClock
	ttl     time.Duration
}

// NewCreatePageToken wires the usecase; ttl is PAYMENT_PAGE_TOKEN_TTL_SECONDS.
func NewCreatePageToken(b outbound.IBookingClient, t outbound.IPageTokenStore, clk clock.IClock, ttl time.Duration) *CreatePageToken {
	return &CreatePageToken{booking: b, tokens: t, clock: clk, ttl: ttl}
}

// CreatePageToken returns the token and the page URL to open in the WebView.
func (u *CreatePageToken) CreatePageToken(ctx context.Context, payer inbound.Payer, bookingID string) (dto.PageToken, error) {
	p, err := loadOwnedPayable(ctx, u.booking, payer, bookingID)
	if err != nil {
		return dto.PageToken{}, err
	}
	now := u.clock.Now()
	if !isPayable(p, now) {
		return dto.PageToken{}, inbound.ErrNotPayable
	}
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return dto.PageToken{}, fmt.Errorf("page token: %w", err)
	}
	tok := base64.RawURLEncoding.EncodeToString(raw[:])
	expires := now.Add(u.ttl).UTC()
	if err := u.tokens.Create(ctx, hashToken(tok), p.BookingID, payer.VisitorID, expires); err != nil {
		return dto.PageToken{}, fmt.Errorf("%w: %w", inbound.ErrDependency, err)
	}
	return dto.PageToken{PageToken: tok, PageURL: "/pay/" + p.BookingID + "?ppt=" + tok + "&embedded=1", ExpiresAt: expires}, nil
}

// ExchangePageToken turns a valid page token into the rb_pay cookie for that visitor and booking.
type ExchangePageToken struct {
	tokens outbound.IPageTokenStore
	signer outbound.ISessionSigner
	clock  clock.IClock
}

// NewExchangePageToken wires the usecase.
func NewExchangePageToken(t outbound.IPageTokenStore, s outbound.ISessionSigner, clk clock.IClock) *ExchangePageToken {
	return &ExchangePageToken{tokens: t, signer: s, clock: clk}
}

// ExchangePageToken consumes the token (once) and signs the session cookie value.
func (u *ExchangePageToken) ExchangePageToken(ctx context.Context, pageToken string) (dto.PageSession, error) {
	if pageToken == "" || len(pageToken) > 128 {
		return dto.PageSession{}, inbound.ErrPageTokenInvalid
	}
	now := u.clock.Now()
	ps, err := u.tokens.Consume(ctx, hashToken(pageToken), now)
	switch {
	case errors.Is(err, outbound.ErrPageTokenInvalid):
		return dto.PageSession{}, inbound.ErrPageTokenInvalid
	case err != nil:
		return dto.PageSession{}, fmt.Errorf("%w: %w", inbound.ErrDependency, err)
	}
	value, expires, err := u.signer.Sign(ps.VisitorID, ps.BookingID, now)
	if err != nil {
		return dto.PageSession{}, fmt.Errorf("page session: %w", err)
	}
	return dto.PageSession{Cookie: value, ExpiresAt: expires}, nil
}

func hashToken(tok string) []byte {
	h := sha256.Sum256([]byte(tok))
	return h[:]
}
