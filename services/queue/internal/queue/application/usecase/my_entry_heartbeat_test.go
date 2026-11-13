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

func meSetup(t *testing.T) (*mock_outbound.MockIQueueStore, *mock_outbound.MockIStatusDocReader, *usecase.GetMyEntry) {
	ctrl := gomock.NewController(t)
	store := mock_outbound.NewMockIQueueStore(ctrl)
	docs := mock_outbound.NewMockIStatusDocReader(ctrl)
	return store, docs, usecase.NewGetMyEntry(store).WithHeartbeat(hbPolicy).WithStatusDocs(docs)
}

// R05.1: zone is computed on SEQ (ADR-030 Amendment: the draw was dropped, seq is the order).
func TestGetMyEntry_SeqAndServerZone(t *testing.T) {
	store, docs, uc := meSetup(t)
	ctx := context.Background()
	store.EXPECT().MyEntry(ctx, "ev1", "v1", int64(2)).Return(outbound.MyEntryResult{
		Tag: outbound.MyEntryOK, Entry: entity.Entry{ID: "e", Seq: 480, Status: valueobject.StatusWaiting}, Position: 37,
	}, nil)
	d := dto.StatusDocument{HeadSeq: i64(1), GuaranteedUntilSeq: i64(3), PossibleUntilSeq: i64(4), PreTurnNotifyPosition: 100}
	docs.EXPECT().Get(ctx, "ev1").Return(docRaw(t, d), d, true, nil)

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
			store, _, uc := meSetup(t)
			ctx := context.Background()
			store.EXPECT().MyEntry(ctx, "ev1", "v1", gomock.Any()).Return(outbound.MyEntryResult{
				Tag: outbound.MyEntryOK, Entry: entity.Entry{ID: "e", Seq: 9, Status: tt.status, EndReason: tt.endReason},
			}, nil)
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
		store, _, uc := meSetup(t)
		store.EXPECT().MyEntry(gomock.Any(), "ev1", "v1", gomock.Any()).Return(outbound.MyEntryResult{Tag: outbound.MyEntryInvalid}, nil)
		_, err := uc.GetMyEntry(context.Background(), inbound.MyEntryInput{EventID: "ev1", VisitorID: "v1", Gen: 2})
		assert.ErrorIs(t, err, entity.ErrVisitorTokenInvalid)
		assert.Equal(t, 401, inbound.MapError(err).HTTPStatus)
		assert.Equal(t, "VISITOR_TOKEN_INVALID", string(inbound.MapError(err).Code))
	})
	t.Run("visitor never joined this event is 404 (REQ-QVT-03)", func(t *testing.T) {
		store, _, uc := meSetup(t)
		store.EXPECT().MyEntry(gomock.Any(), "ev1", "v1", gomock.Any()).Return(outbound.MyEntryResult{Tag: outbound.MyEntryAbsent}, nil)
		_, err := uc.GetMyEntry(context.Background(), inbound.MyEntryInput{EventID: "ev1", VisitorID: "v1", Gen: 1})
		assert.ErrorIs(t, err, entity.ErrNotFound)
		assert.Equal(t, 404, inbound.MapError(err).HTTPStatus)
	})
	t.Run("cross-event: record exists for A only, event B is 404 and reads no entry", func(t *testing.T) {
		store, _, uc := meSetup(t)
		store.EXPECT().MyEntry(gomock.Any(), "evB", "v1", gomock.Any()).Return(outbound.MyEntryResult{Tag: outbound.MyEntryAbsent}, nil)
		_, err := uc.GetMyEntry(context.Background(), inbound.MyEntryInput{EventID: "evB", VisitorID: "v1", Gen: 1})
		assert.ErrorIs(t, err, entity.ErrNotFound)
	})
	t.Run("valid token, no entry ever: 404", func(t *testing.T) {
		store, _, uc := meSetup(t)
		store.EXPECT().MyEntry(gomock.Any(), "ev1", "v1", gomock.Any()).Return(outbound.MyEntryResult{Tag: outbound.MyEntryNotFound}, nil)
		_, err := uc.GetMyEntry(context.Background(), inbound.MyEntryInput{EventID: "ev1", VisitorID: "v1", Gen: 1})
		assert.ErrorIs(t, err, entity.ErrNotFound)
	})
}

func TestHeartbeat_Outcomes(t *testing.T) {
	ctx := context.Background()
	newUC := func(t *testing.T) (*mock_outbound.MockIQueueStore, *mock_outbound.MockIStatusDocReader, *usecase.Heartbeat) {
		ctrl := gomock.NewController(t)
		store := mock_outbound.NewMockIQueueStore(ctrl)
		docs := mock_outbound.NewMockIStatusDocReader(ctrl)
		return store, docs, usecase.NewHeartbeat(store, fixedNow, hbPolicy).WithStatusDocs(docs)
	}
	t.Run("ok near the head gets the short interval", func(t *testing.T) {
		store, docs, uc := newUC(t)
		store.EXPECT().Heartbeat(ctx, "ev1", "v1", int64(2), gomock.Any()).Return(outbound.HeartbeatResult{Tag: outbound.HeartbeatOK, Status: "WAITING", Seq: 120}, nil)
		d := dto.StatusDocument{HeadSeq: i64(100), PreTurnNotifyPosition: 50}
		docs.EXPECT().Get(ctx, "ev1").Return(docRaw(t, d), d, true, nil)
		got, err := uc.Heartbeat(ctx, inbound.HeartbeatInput{EventID: "ev1", VisitorID: "v1", Gen: 2})
		require.NoError(t, err)
		assert.Equal(t, "WAITING", got.State)
		assert.EqualValues(t, 10_000, got.NextHeartbeatMs)
		require.NotNil(t, got.Rank)
		assert.EqualValues(t, 120, *got.Rank)
	})
	t.Run("invalid token", func(t *testing.T) {
		store, _, uc := newUC(t)
		store.EXPECT().Heartbeat(ctx, "ev1", "v1", int64(1), gomock.Any()).Return(outbound.HeartbeatResult{Tag: outbound.HeartbeatInvalid}, nil)
		_, err := uc.Heartbeat(ctx, inbound.HeartbeatInput{EventID: "ev1", VisitorID: "v1", Gen: 1})
		assert.ErrorIs(t, err, entity.ErrVisitorTokenInvalid)
	})
	t.Run("ended entry", func(t *testing.T) {
		store, _, uc := newUC(t)
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
	store := mock_outbound.NewMockIQueueStore(ctrl) // only MyEntry (me) may be read; Heartbeat must not be called
	limiter := mock_outbound.NewMockIRateLimiter(ctrl)
	ctx := context.Background()
	window := time.Minute

	limiter.EXPECT().Allow(ctx, "hb:v:ev1:v1", 20, window).Return(false, nil)
	_, err := usecase.NewHeartbeat(store, fixedNow, hbPolicy).WithLimiter(limiter, 20, window).
		Heartbeat(ctx, inbound.HeartbeatInput{EventID: "ev1", VisitorID: "v1", Gen: 1})
	var ae *apperror.Error
	require.ErrorAs(t, err, &ae)
	assert.Equal(t, 429, ae.HTTPStatus)

	store.EXPECT().MyEntry(ctx, "ev1", "v1", gomock.Any()).Return(outbound.MyEntryResult{Tag: outbound.MyEntryOK}, nil)
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

// C8.9: `me` costs one store call (me.lua) and reads the document through the process cache; the rate limit is
// checked on the same call's result. Any further store read fails the strict mock.
func TestGetMyEntry_OneStoreCall(t *testing.T) {
	store, docs, uc := meSetup(t)
	limiter := mock_outbound.NewMockIRateLimiter(gomock.NewController(t))
	ctx := context.Background()

	store.EXPECT().MyEntry(ctx, "ev1", "v1", int64(3)).Return(outbound.MyEntryResult{
		Tag: outbound.MyEntryOK, Entry: entity.Entry{ID: "e", Seq: 5, Status: valueobject.StatusWaiting}, Position: 3,
	}, nil)
	limiter.EXPECT().Allow(ctx, "me:v:ev1:v1", 60, time.Minute).Return(true, nil)
	d := dto.StatusDocument{PreTurnNotifyPosition: 50}
	docs.EXPECT().Get(ctx, "ev1").Return(docRaw(t, d), d, true, nil)

	got, err := uc.WithLimiter(limiter, 60, time.Minute).GetMyEntry(ctx, inbound.MyEntryInput{EventID: "ev1", VisitorID: "v1", Gen: 3})
	require.NoError(t, err)
	assert.EqualValues(t, 3, got.ExactPosition)
	assert.Equal(t, "WAITING", got.State)
}

// `me` returns the poll interval tiered on exactPosition by the published poll policy, the default policy when no
// document is published, and nothing once the entry has ENDED.
func TestGetMyEntry_NextPollAfterMs(t *testing.T) {
	poll := dto.Poll{FarMs: 20_000, MidMs: 8_000, NearMs: 2_000, FarAbove: 1000, MidAbove: 50}
	tests := []struct {
		name   string
		status valueobject.Status
		pos    int64
		doc    *dto.StatusDocument
		want   int64
	}{
		{"far", valueobject.StatusWaiting, 1001, &dto.StatusDocument{Poll: poll}, 20_000},
		{"mid", valueobject.StatusWaiting, 51, &dto.StatusDocument{Poll: poll}, 8_000},
		{"near", valueobject.StatusWaiting, 50, &dto.StatusDocument{Poll: poll}, 2_000},
		{"stale is tiered too", valueobject.StatusStale, 2000, &dto.StatusDocument{Poll: poll}, 20_000},
		{"admitted polls near", valueobject.StatusAdmitted, 0, &dto.StatusDocument{Poll: poll}, 2_000},
		{"no document uses defaults", valueobject.StatusWaiting, 501, nil, 30_000},
		{"ended stops polling", valueobject.StatusLeft, 0, nil, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store, docs, uc := meSetup(t)
			ctx := context.Background()
			store.EXPECT().MyEntry(ctx, "ev1", "v1", int64(1)).Return(outbound.MyEntryResult{
				Tag: outbound.MyEntryOK, Entry: entity.Entry{ID: "e", Seq: 9, Status: tt.status}, Position: tt.pos,
			}, nil)
			switch {
			case tt.status.IsTerminal():
			case tt.doc == nil:
				docs.EXPECT().Get(ctx, "ev1").Return(nil, dto.StatusDocument{}, false, nil)
			default:
				docs.EXPECT().Get(ctx, "ev1").Return(docRaw(t, *tt.doc), *tt.doc, true, nil)
			}
			got, err := uc.GetMyEntry(ctx, inbound.MyEntryInput{EventID: "ev1", VisitorID: "v1", Gen: 1})
			require.NoError(t, err)
			assert.Equal(t, tt.want, got.NextPollAfterMs)
		})
	}
}
