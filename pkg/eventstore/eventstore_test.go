package eventstore

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeDB struct {
	inserts []([]any)
	failAt  int
	failErr error
}

func (f *fakeDB) Exec(_ context.Context, _ string, args ...any) (pgconn.CommandTag, error) {
	f.inserts = append(f.inserts, args)
	if f.failAt > 0 && len(f.inserts) == f.failAt {
		return pgconn.CommandTag{}, f.failErr
	}
	return pgconn.NewCommandTag("INSERT 0 1"), nil
}

func (f *fakeDB) Query(context.Context, string, ...any) (pgx.Rows, error) {
	return nil, errors.New("not used")
}

func TestNew(t *testing.T) {
	_, err := New("booking_events")
	assert.NoError(t, err)
	_, err = New("bookings")
	assert.Error(t, err)
	_, err = New("x_events; drop")
	assert.Error(t, err)
}

func TestAppend_VersionsAndDefaults(t *testing.T) {
	s, _ := New("booking_events")
	db := &fakeDB{}
	v, err := s.Append(context.Background(), db, "bk-1", 2,
		NewEvent{Type: "BookingItemsReplaced", Payload: map[string]any{"items": 2}},
		NewEvent{Type: "BookingLocked", SchemaVersion: 2, Payload: map[string]any{"reason": "PAYMENT_STARTED"}, Metadata: map[string]string{"correlationId": "c1"}},
	)
	require.NoError(t, err)
	assert.Equal(t, 4, v)
	require.Len(t, db.inserts, 2)
	assert.Equal(t, 3, db.inserts[0][1])
	assert.Equal(t, 1, db.inserts[0][3])
	assert.Equal(t, 4, db.inserts[1][1])
	assert.Equal(t, 2, db.inserts[1][3])
	assert.JSONEq(t, `{"correlationId":"c1"}`, string(db.inserts[1][5].([]byte)))
	assert.JSONEq(t, `{}`, string(db.inserts[0][5].([]byte)))
}

func TestAppend_NoEventsIsNoop(t *testing.T) {
	s, _ := New("booking_events")
	v, err := s.Append(context.Background(), &fakeDB{}, "bk-1", 7)
	require.NoError(t, err)
	assert.Equal(t, 7, v)
}

func TestAppend_ConflictMapsToErrConcurrency(t *testing.T) {
	s, _ := New("booking_events")
	db := &fakeDB{failAt: 1, failErr: &pgconn.PgError{Code: "23505"}}
	_, err := s.Append(context.Background(), db, "bk-1", 0, NewEvent{Type: "BookingHeld", Payload: map[string]int{"n": 1}})
	assert.ErrorIs(t, err, ErrConcurrency)

	db = &fakeDB{failAt: 1, failErr: errors.New("conn reset")}
	_, err = s.Append(context.Background(), db, "bk-1", 0, NewEvent{Type: "BookingHeld", Payload: map[string]int{"n": 1}})
	assert.Error(t, err)
	assert.NotErrorIs(t, err, ErrConcurrency)
}

func TestAppend_RejectsBadInput(t *testing.T) {
	s, _ := New("booking_events")
	ctx := context.Background()
	tests := []struct {
		name string
		ev   NewEvent
		want error
	}{
		{"plaintext nik", NewEvent{Type: "BookingHeld", Payload: map[string]any{"items": []any{map[string]any{"attendee_nik": "3174..."}}}}, ErrForbiddenField},
		{"email", NewEvent{Type: "BookingHeld", Payload: map[string]any{"buyerEmail": "a@b.c"}}, ErrForbiddenField},
		{"token", NewEvent{Type: "PaymentPageOpened", Payload: map[string]any{"token": "x"}}, ErrForbiddenField},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := s.Append(ctx, &fakeDB{}, "bk-1", 0, tt.ev)
			assert.ErrorIs(t, err, tt.want)
		})
	}

	_, err := s.Append(ctx, &fakeDB{}, "bk-1", 0, NewEvent{Type: "booking_held", Payload: 1})
	assert.ErrorContains(t, err, "PascalCase")
	_, err = s.Append(ctx, &fakeDB{}, "bk-1", -1, NewEvent{Type: "BookingHeld", Payload: 1})
	assert.Error(t, err)
	_, err = s.Append(ctx, &fakeDB{}, "bk-1", 0, NewEvent{Type: "BookingHeld", Payload: func() {}})
	assert.Error(t, err)
}

func TestAppend_AllowsHashedAndMaskedFields(t *testing.T) {
	s, _ := New("booking_events")
	_, err := s.Append(context.Background(), &fakeDB{}, "bk-1", 0, NewEvent{Type: "BookingHeld",
		Payload: map[string]any{"buyerNikHash": "ab12", "attendeeNikMasked": "3174********0001"}})
	assert.NoError(t, err)
}

type counter struct {
	status string
	seen   []string
	fail   string
}

func (c *counter) Apply(e Event) error {
	if e.Type == c.fail {
		return errors.New("unknown event")
	}
	c.seen = append(c.seen, e.Type)
	var p struct{ Status string }
	_ = json.Unmarshal(e.Payload, &p)
	if p.Status != "" {
		c.status = p.Status
	}
	return nil
}

func TestReplay(t *testing.T) {
	events := []Event{
		{StreamID: "bk", Version: 1, Type: "BookingHeld", Payload: json.RawMessage(`{"Status":"PENDING_PAYMENT"}`)},
		{StreamID: "bk", Version: 2, Type: "BookingLocked", Payload: json.RawMessage(`{}`)},
		{StreamID: "bk", Version: 3, Type: "BookingPaid", Payload: json.RawMessage(`{"Status":"PAID"}`)},
	}
	c := &counter{}
	v, err := Replay(events, c)
	require.NoError(t, err)
	assert.Equal(t, 3, v)
	assert.Equal(t, "PAID", c.status)
	assert.Equal(t, []string{"BookingHeld", "BookingLocked", "BookingPaid"}, c.seen)
}

func TestReplay_DetectsGapAndApplyError(t *testing.T) {
	_, err := Replay([]Event{{StreamID: "bk", Version: 1, Type: "A"}, {StreamID: "bk", Version: 3, Type: "B"}}, &counter{fail: "none"})
	assert.ErrorContains(t, err, "melompat")

	v, err := Replay([]Event{{StreamID: "bk", Version: 1, Type: "A"}, {StreamID: "bk", Version: 2, Type: "B"}}, &counter{fail: "B"})
	assert.Error(t, err)
	assert.Equal(t, 1, v)
}
