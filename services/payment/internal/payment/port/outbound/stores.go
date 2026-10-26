package outbound

import (
	"context"
	"errors"
	"time"
)

//go:generate go tool mockgen -source=stores.go -destination=../../../../_mock/payment/port/outbound/stores_mock.go -package=mock_outbound

// ErrPageTokenInvalid means the page token is unknown, used, or expired.
var ErrPageTokenInvalid = errors.New("page token: tidak valid")

// Callback is one webhook delivery kept for audit (payment_callbacks).
type Callback struct {
	Provider         string
	Fingerprint      []byte
	ProviderOrderID  string
	NormalizedStatus string
	SignatureValid   bool
	AmountMinor      *int64
	PayloadDigest    []byte
	Result           string // PROCESSED | DUPLICATE | REJECTED | ERROR
}

// Callback processing results (payment_callbacks CHECK).
const (
	CallbackProcessed = "PROCESSED"
	CallbackDuplicate = "DUPLICATE"
	CallbackRejected  = "REJECTED"
	CallbackError     = "ERROR"
)

// ICallbackStore records webhooks. A repeated (provider, fingerprint) is stored once.
type ICallbackStore interface {
	Record(ctx context.Context, cb Callback) (inserted bool, err error)
}

// IdempotencyRecord is a stored request (idempotency_records).
type IdempotencyRecord struct {
	State        string // IN_PROGRESS | COMPLETED
	RequestHash  []byte
	ResponseBody []byte
}

// IIdempotencyStore guards POST attempts. Begin claims the key; Release drops it when the request failed so the
// client can retry with the same key.
type IIdempotencyStore interface {
	Begin(ctx context.Context, scope, ownerID, key string, requestHash []byte, ttl time.Duration) (rec IdempotencyRecord, found bool, err error)
	Complete(ctx context.Context, scope, ownerID, key string, status int, body []byte) error
	Release(ctx context.Context, scope, ownerID, key string) error
}

// PageSession is who a consumed page token belongs to.
type PageSession struct {
	BookingID string
	VisitorID string
}

// IPageTokenStore keeps one-time WebView tokens (only the hash is stored).
type IPageTokenStore interface {
	Create(ctx context.Context, tokenHash []byte, bookingID, visitorID string, expiresAt time.Time) error
	// Consume marks the token used and returns its owner; ErrPageTokenInvalid when unknown, used, or expired.
	Consume(ctx context.Context, tokenHash []byte, now time.Time) (PageSession, error)
}

// ISessionSigner signs the rb_pay cookie value for one visitor and booking.
type ISessionSigner interface {
	Sign(visitorID, bookingID string, now time.Time) (value string, expiresAt time.Time, err error)
}
