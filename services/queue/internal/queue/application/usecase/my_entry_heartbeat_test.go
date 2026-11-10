package usecase_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/apperror"

	mock_outbound "github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/_mock/queue/port/outbound"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/application/usecase"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/domain/dto"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/domain/entity"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/domain/valueobject"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/port/inbound"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/port/outbound"
)

var hbPolicy = usecase.HeartbeatPolicy{BaseMs: 30_000, NearMs: 10_000, Jitter: func() float64 { return 0.5 }}

func i64(v int64) *int64 { return &v }

func docRaw(t *testing.T, d dto.StatusDocument) []byte {
	t.Helper()
	raw, err := json.Marshal(d)
	require.NoError(t, err)
	return raw
}

func meSetup(t *testing.T) (*mock_outbound.MockIQueueStore, *usecase.GetMyEntry) {
	ctrl := gomock.NewController(t)
	store := mock_outbound.NewMockIQueueStore(ctrl)
	return store, usecase.NewGetMyEntry(store).WithHeartbeat(hbPolicy)
}

// R05.1: zone is computed on SEQ (ADR-030 Amendment: the draw was dropped, seq is the order).
func TestGetMyEntry_SeqAndServerZone(t *testing.T) {
	store, uc := meSetup(t)
	ctx := context.Background()
	store.EXPECT().TokenGen(ctx, "ev1", "v1").Return(int64(2), true, nil)
	store.EXPECT().FindByVisitor(ctx, "ev1", "v1").Return(entity.Entry{ID: "e", Seq: 480, Status: valueobject.StatusWaiting}, true, nil)
	store.EXPECT().GetStatusDoc(ctx, "ev1").Return(docRaw(t, dto.StatusDocument{HeadSeq: i64(1), GuaranteedUntilSeq: i64(3), PossibleUntilSeq: i64(4), PreTurnNotifyPosition: 100}), true, nil)
	store.EXPECT().LivePosition(ctx, "ev1", int64(480)).Return(int64(37), nil)

	got, err := uc.GetMyEntry(ctx, inbound.MyEntryInput{EventID: "ev1", VisitorID: "v1", Gen: 2})
	require.NoError(t, err)
	require.NotNil(t, got.Rank)
	assert.EqualValues(t, 480, *got.Rank, "rank is the seq (strict FIFO)")
	require.NotNil(t, got.Zone)
	assert.Equal(t, "NOT_ENOUGH", *got.Zone)
	assert.EqualValues(t, 37, got.ExactPosition)
	assert.Equal(t, "WAITING", got.State)
	assert.EqualValues(t, 30_000, got.NextHeartbeatMs)
}

// Addendum 2: an ENDED entry is returned with state ENDED and a machine-readable reason, not 404.
func TestGetMyEntry_EndedReasons(t *testing.T) {
	tests := []struct {
		status    valueobject.Status
		endReason string
		reason    string
	}{
		{valueobject.StatusLeft, "LEFT", "LEFT"},
		{valueobject.StatusEnded, "STALE_TIMEOUT", "STALE_TIMEOUT"},
		{valueobject.StatusMissed, "SWEEP_MISSED", "CLAIM_WINDOW_EXPIRED"},
		{valueobject.StatusMissed, "UNCLAIMED", "CLAIM_WINDOW_EXPIRED"},
		{valueobject.StatusEnded, "EXPIRED", "PAGE_TIMER_EXPIRED"},
		{valueobject.StatusEnded, "SESSION_EXPIRED", "PAGE_TIMER_EXPIRED"},
		{valueobject.StatusCompleted, "PAID", "JOURNEY_ENDED"},
		{valueobject.StatusEnded, "ALREADY_ACTIVE", "JOURNEY_ENDED"},
		{valueobject.StatusSoldOut, "SOLD_OUT", "SOLD_OUT"},
	}
	for _, tt := range tests {
		t.Run(tt.endReason, func(t *testing.T) {
			store, uc := meSetup(t)
			ctx := context.Background()
			store.EXPECT().TokenGen(ctx, "ev1", "v1").Return(int64(1), true, nil)
			store.EXPECT().FindByVisitor(ctx, "ev1", "v1").Return(entity.Entry{}, false, nil)
			store.EXPECT().FindLastByVisitor(ctx, "ev1", "v1").Return(entity.Entry{ID: "e", Seq: 9, Status: tt.status, EndReason: tt.endReason}, true, nil)
			got, err := uc.GetMyEntry(ctx, inbound.MyEntryInput{EventID: "ev1", VisitorID: "v1", Gen: 1})
			require.NoError(t, err)
			assert.Equal(t, "ENDED", got.State)
			assert.Equal(t, tt.reason, got.Reason)
			assert.Nil(t, got.Rank)
		})
	}
}

func TestGetMyEntry_TokenStates(t *testing.T) {
	t.Run("rotated-out token is 401", func(t *testing.T) {
		store, uc := meSetup(t)
		store.EXPECT().TokenGen(gomock.Any(), "ev1", "v1").Return(int64(3), true, nil)
		_, err := uc.GetMyEntry(context.Background(), inbound.MyEntryInput{EventID: "ev1", VisitorID: "v1", Gen: 2})
		assert.ErrorIs(t, err, entity.ErrVisitorTokenInvalid)
		assert.Equal(t, 401, inbound.MapError(err).HTTPStatus)
		assert.Equal(t, "VISITOR_TOKEN_INVALID", string(inbound.MapError(err).Code))
	})
	t.Run("visitor never joined this event is 404 (REQ-QVT-03)", func(t *testing.T) {
		store, uc := meSetup(t)
		store.EXPECT().TokenGen(gomock.Any(), "ev1", "v1").Return(int64(0), false, nil)
		_, err := uc.GetMyEntry(context.Background(), inbound.MyEntryInput{EventID: "ev1", VisitorID: "v1", Gen: 1})
		assert.ErrorIs(t, err, entity.ErrNotFound)
		assert.Equal(t, 404, inbound.MapError(err).HTTPStatus)
	})
	t.Run("cross-event: record exists for A only, event B is 404 and reads no entry", func(t *testing.T) {
		store, uc := meSetup(t)
		store.EXPECT().TokenGen(gomock.Any(), "evB", "v1").Return(int64(0), false, nil)
		_, err := uc.GetMyEntry(context.Background(), inbound.MyEntryInput{EventID: "evB", VisitorID: "v1", Gen: 1})
		assert.ErrorIs(t, err, entity.ErrNotFound)
	})
	t.Run("valid token, no entry ever: 404", func(t *testing.T) {
		store, uc := meSetup(t)
		store.EXPECT().TokenGen(gomock.Any(), "ev1", "v1").Return(int64(1), true, nil)
		store.EXPECT().FindByVisitor(gomock.Any(), "ev1", "v1").Return(entity.Entry{}, false, nil)
		store.EXPECT().FindLastByVisitor(gomock.Any(), "ev1", "v1").Return(entity.Entry{}, false, nil)
		_, err := uc.GetMyEntry(context.Background(), inbound.MyEntryInput{EventID: "ev1", VisitorID: "v1", Gen: 1})
		assert.ErrorIs(t, err, entity.ErrNotFound)
	})
}

func TestHeartbeat_Outcomes(t *testing.T) {
	ctx := context.Background()
	newUC := func(t *testing.T) (*mock_outbound.MockIQueueStore, *usecase.Heartbeat) {
		ctrl := gomock.NewController(t)
		store := mock_outbound.NewMockIQueueStore(ctrl)
		return store, usecase.NewHeartbeat(store, fixedNow, hbPolicy)
	}
	t.Run("ok near the head gets the short interval", func(t *testing.T) {
		store, uc := newUC(t)
		store.EXPECT().Heartbeat(ctx, "ev1", "v1", int64(2), gomock.Any()).Return(outbound.HeartbeatResult{Tag: outbound.HeartbeatOK, Status: "WAITING", Seq: 120}, nil)
		store.EXPECT().GetStatusDoc(ctx, "ev1").Return(docRaw(t, dto.StatusDocument{HeadSeq: i64(100), PreTurnNotifyPosition: 50}), true, nil)
		got, err := uc.Heartbeat(ctx, inbound.HeartbeatInput{EventID: "ev1", VisitorID: "v1", Gen: 2})
		require.NoError(t, err)
		assert.Equal(t, "WAITING", got.State)
		assert.EqualValues(t, 10_000, got.NextHeartbeatMs)
		require.NotNil(t, got.Rank)
		assert.EqualValues(t, 120, *got.Rank)
	})
	t.Run("invalid token", func(t *testing.T) {
		store, uc := newUC(t)
		store.EXPECT().Heartbeat(ctx, "ev1", "v1", int64(1), gomock.Any()).Return(outbound.HeartbeatResult{Tag: outbound.HeartbeatInvalid}, nil)
		_, err := uc.Heartbeat(ctx, inbound.HeartbeatInput{EventID: "ev1", VisitorID: "v1", Gen: 1})
		assert.ErrorIs(t, err, entity.ErrVisitorTokenInvalid)
	})
	t.Run("ended entry", func(t *testing.T) {
		store, uc := newUC(t)
		store.EXPECT().Heartbeat(ctx, "ev1", "v1", int64(1), gomock.Any()).Return(outbound.HeartbeatResult{Tag: outbound.HeartbeatNotFound}, nil)
		_, err := uc.Heartbeat(ctx, inbound.HeartbeatInput{EventID: "ev1", VisitorID: "v1", Gen: 1})
		assert.ErrorIs(t, err, entity.ErrEntryNotFound)
		assert.Equal(t, 404, inbound.MapError(err).HTTPStatus)
		assert.Equal(t, "ENTRY_NOT_FOUND", string(inbound.MapError(err).Code))
	})
}

// ADR-030 R03: heartbeat and `me` are capped per visitor; beyond the cap the store is never touched and the answer
// is a 429 RATE_LIMITED with a Retry-After hint. A limiter error is fail-closed.
func TestHeartbeatAndMe_PerVisitorLimit(t *testing.T) {
	ctrl := gomock.NewController(t)
	store := mock_outbound.NewMockIQueueStore(ctrl) // only TokenGen (me) may be read; Heartbeat/Find* must not be called
	limiter := mock_outbound.NewMockIRateLimiter(ctrl)
	ctx := context.Background()
	window := time.Minute

	limiter.EXPECT().Allow(ctx, "hb:v:ev1:v1", 20, window).Return(false, nil)
	_, err := usecase.NewHeartbeat(store, fixedNow, hbPolicy).WithLimiter(limiter, 20, window).
		Heartbeat(ctx, inbound.HeartbeatInput{EventID: "ev1", VisitorID: "v1", Gen: 1})
	var ae *apperror.Error
	require.ErrorAs(t, err, &ae)
	assert.Equal(t, 429, ae.HTTPStatus)

	store.EXPECT().TokenGen(ctx, "ev1", "v1").Return(int64(1), true, nil)
	limiter.EXPECT().Allow(ctx, "me:v:ev1:v1", 120, window).Return(false, nil)
	_, err = usecase.NewGetMyEntry(store).WithLimiter(limiter, 120, window).
		GetMyEntry(ctx, inbound.MyEntryInput{EventID: "ev1", VisitorID: "v1", Gen: 1})
	require.ErrorAs(t, err, &ae)
	assert.Equal(t, 429, ae.HTTPStatus)

	limiter.EXPECT().Allow(ctx, "hb:v:ev1:v1", 20, window).Return(false, errors.New("valkey down"))
	_, err = usecase.NewHeartbeat(store, fixedNow, hbPolicy).WithLimiter(limiter, 20, window).
		Heartbeat(ctx, inbound.HeartbeatInput{EventID: "ev1", VisitorID: "v1", Gen: 1})
	require.Error(t, err)
}
