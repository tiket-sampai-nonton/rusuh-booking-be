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

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/clock"

	mock_outbound "github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/_mock/queue/port/outbound"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/application/usecase"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/domain/dto"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/domain/entity"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/domain/valueobject"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/port/inbound"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/port/outbound"
)

var fixedNow = clock.Fixed{T: time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC)}

func TestClaim_Success(t *testing.T) {
	ctrl := gomock.NewController(t)
	store := mock_outbound.NewMockIQueueStore(ctrl)
	tokens := mock_outbound.NewMockITokenIssuer(ctrl)
	ctx := context.Background()

	store.EXPECT().Claim(ctx, "ev1", "e1", "v1", gomock.Any()).Return(outbound.ClaimResult{Status: "CLAIMED"}, nil)
	// Claim usecase reads allocationId from Valkey after claim.lua
	store.EXPECT().GetEntry(ctx, "ev1", "e1").Return(entity.Entry{AllocationID: "alloc-1"}, true, nil)
	tokens.EXPECT().IssueAdmission("e1", "ev1", "alloc-1", fixedNow.T).Return("adm-token", fixedNow.T.Add(time.Minute), nil)

	uc := usecase.NewClaimTurn(store, tokens, fixedNow, "https://app/book")
	resp, err := uc.Claim(ctx, inbound.ClaimInput{EntryID: "e1", VisitorID: "v1", EventID: "ev1"})
	require.NoError(t, err)
	assert.Equal(t, "adm-token", resp.AdmissionToken)
	assert.Equal(t, "https://app/book/e1", resp.BookingURL)
}

func TestClaim_TurnMissed(t *testing.T) {
	ctrl := gomock.NewController(t)
	store := mock_outbound.NewMockIQueueStore(ctrl)
	tokens := mock_outbound.NewMockITokenIssuer(ctrl)
	ctx := context.Background()
	store.EXPECT().Claim(ctx, "ev1", "e1", "v1", gomock.Any()).Return(outbound.ClaimResult{}, entity.ErrTurnMissed)

	uc := usecase.NewClaimTurn(store, tokens, fixedNow, "https://app/book")
	_, err := uc.Claim(ctx, inbound.ClaimInput{EntryID: "e1", VisitorID: "v1", EventID: "ev1"})
	assert.ErrorIs(t, err, entity.ErrTurnMissed)
}

func TestLeave_WaitingSkipsRelease(t *testing.T) {
	ctrl := gomock.NewController(t)
	store := mock_outbound.NewMockIQueueStore(ctrl)
	booking := mock_outbound.NewMockIBookingClient(ctrl)
	ctx := context.Background()
	// Entry is WAITING — no booking release call expected.
	store.EXPECT().GetEntry(ctx, "ev1", "e1").Return(entity.Entry{ID: "e1", Status: valueobject.StatusWaiting}, true, nil)
	store.EXPECT().Leave(ctx, "ev1", "e1", "v1", gomock.Any()).Return(true, nil)

	uc := usecase.NewLeaveQueue(store, booking, fixedNow)
	err := uc.Leave(ctx, inbound.LeaveInput{EntryID: "e1", VisitorID: "v1", EventID: "ev1"})
	require.NoError(t, err)
}

func TestLeave_AdmittedCallsRelease(t *testing.T) {
	ctrl := gomock.NewController(t)
	store := mock_outbound.NewMockIQueueStore(ctrl)
	booking := mock_outbound.NewMockIBookingClient(ctrl)
	ctx := context.Background()
	// Entry is ADMITTED — booking release must be called with correct allocationID and reason.
	store.EXPECT().GetEntry(ctx, "ev1", "e1").Return(entity.Entry{
		ID: "e1", Status: valueobject.StatusAdmitted, AllocationID: "alloc-42",
	}, true, nil)
	booking.EXPECT().ReleaseAllocation(ctx, "alloc-42", "QUEUE_ENTRY_GONE").Return(nil)
	store.EXPECT().Leave(ctx, "ev1", "e1", "v1", gomock.Any()).Return(true, nil)

	uc := usecase.NewLeaveQueue(store, booking, fixedNow)
	err := uc.Leave(ctx, inbound.LeaveInput{EntryID: "e1", VisitorID: "v1", EventID: "ev1"})
	require.NoError(t, err)
}

func TestLeave_AdmittedReleaseFailureContinues(t *testing.T) {
	ctrl := gomock.NewController(t)
	store := mock_outbound.NewMockIQueueStore(ctrl)
	booking := mock_outbound.NewMockIBookingClient(ctrl)
	ctx := context.Background()
	store.EXPECT().GetEntry(ctx, "ev1", "e1").Return(entity.Entry{
		ID: "e1", Status: valueobject.StatusAdmitted, AllocationID: "alloc-42",
	}, true, nil)
	// Release fails — Leave still proceeds (best-effort, spec 14.R1 K7).
	booking.EXPECT().ReleaseAllocation(ctx, "alloc-42", "QUEUE_ENTRY_GONE").Return(errors.New("booking down"))
	store.EXPECT().Leave(ctx, "ev1", "e1", "v1", gomock.Any()).Return(true, nil)

	uc := usecase.NewLeaveQueue(store, booking, fixedNow)
	err := uc.Leave(ctx, inbound.LeaveInput{EntryID: "e1", VisitorID: "v1", EventID: "ev1"})
	require.NoError(t, err) // leave still succeeds
}

func TestLeave_NotFound(t *testing.T) {
	ctrl := gomock.NewController(t)
	store := mock_outbound.NewMockIQueueStore(ctrl)
	booking := mock_outbound.NewMockIBookingClient(ctrl)
	ctx := context.Background()
	store.EXPECT().GetEntry(ctx, "ev1", "e1").Return(entity.Entry{}, false, nil)
	store.EXPECT().Leave(ctx, "ev1", "e1", "v1", gomock.Any()).Return(false, entity.ErrNotFound)

	uc := usecase.NewLeaveQueue(store, booking, fixedNow)
	err := uc.Leave(ctx, inbound.LeaveInput{EntryID: "e1", VisitorID: "v1", EventID: "ev1"})
	assert.ErrorIs(t, err, entity.ErrNotFound)
}

func TestGetStatus_Found(t *testing.T) {
	ctrl := gomock.NewController(t)
	store := mock_outbound.NewMockIQueueStore(ctrl)
	ctx := context.Background()
	doc := dto.StatusDocument{EventID: "ev1", SaleState: "OPEN", ServingSeq: 100, SoldOut: false}
	raw, _ := json.Marshal(doc)
	store.EXPECT().GetStatusDoc(ctx, "ev1").Return(raw, true, nil)

	uc := usecase.NewGetStatus(store)
	got, err := uc.GetStatus(ctx, "ev1")
	require.NoError(t, err)
	assert.Equal(t, doc, got)
}

func TestGetStatus_NotFound(t *testing.T) {
	ctrl := gomock.NewController(t)
	store := mock_outbound.NewMockIQueueStore(ctrl)
	ctx := context.Background()
	store.EXPECT().GetStatusDoc(ctx, "ev1").Return(nil, false, nil)

	uc := usecase.NewGetStatus(store)
	_, err := uc.GetStatus(ctx, "ev1")
	assert.ErrorIs(t, err, entity.ErrNotFound)
}

func TestGetStatus_StorageError(t *testing.T) {
	ctrl := gomock.NewController(t)
	store := mock_outbound.NewMockIQueueStore(ctrl)
	ctx := context.Background()
	store.EXPECT().GetStatusDoc(ctx, "ev1").Return(nil, false, errors.New("boom"))

	uc := usecase.NewGetStatus(store)
	_, err := uc.GetStatus(ctx, "ev1")
	assert.Error(t, err)
}

func TestGetMyEntry_WithStatusContext(t *testing.T) {
	ctrl := gomock.NewController(t)
	store := mock_outbound.NewMockIQueueStore(ctrl)
	ctx := context.Background()

	ent := entity.Entry{ID: "e1", Seq: valueobject.Seq(100), Status: valueobject.StatusWaiting, VisitorID: "v1"}
	store.EXPECT().FindByVisitor(ctx, "ev1", "v1").Return(ent, true, nil)
	doc := dto.StatusDocument{ServingSeq: 55, GuaranteedUntilSeq: 150, PossibleUntilSeq: 200}
	raw, _ := json.Marshal(doc)
	store.EXPECT().GetStatusDoc(ctx, "ev1").Return(raw, true, nil)

	uc := usecase.NewGetMyEntry(store)
	got, err := uc.GetMyEntry(ctx, inbound.MyEntryInput{EventID: "ev1", VisitorID: "v1"})
	require.NoError(t, err)
	assert.Equal(t, int64(45), got.ExactPosition)
	assert.Equal(t, "GUARANTEED", got.Zone)
}

func TestGetMyEntry_RemovedAhead(t *testing.T) {
	ctrl := gomock.NewController(t)
	store := mock_outbound.NewMockIQueueStore(ctrl)
	ctx := context.Background()

	ent := entity.Entry{ID: "e1", Seq: valueobject.Seq(100), Status: valueobject.StatusWaiting, VisitorID: "v1"}
	store.EXPECT().FindByVisitor(ctx, "ev1", "v1").Return(ent, true, nil)
	doc := dto.StatusDocument{
		ServingSeq: 55, GuaranteedUntilSeq: 150, PossibleUntilSeq: 200,
		RemovedBuckets: &dto.RemovedBuckets{Size: 10, FromSeq: 0, Counts: []int64{2, 3}}, // buckets starting 0,10 both < 100
	}
	raw, _ := json.Marshal(doc)
	store.EXPECT().GetStatusDoc(ctx, "ev1").Return(raw, true, nil)

	uc := usecase.NewGetMyEntry(store)
	got, err := uc.GetMyEntry(ctx, inbound.MyEntryInput{EventID: "ev1", VisitorID: "v1"})
	require.NoError(t, err)
	assert.Equal(t, int64(40), got.ExactPosition) // 100-55-(2+3)
}

func TestGetMyEntry_NotFound(t *testing.T) {
	ctrl := gomock.NewController(t)
	store := mock_outbound.NewMockIQueueStore(ctrl)
	ctx := context.Background()
	store.EXPECT().FindByVisitor(ctx, "ev1", "v1").Return(entity.Entry{}, false, nil)

	uc := usecase.NewGetMyEntry(store)
	_, err := uc.GetMyEntry(ctx, inbound.MyEntryInput{EventID: "ev1", VisitorID: "v1"})
	assert.ErrorIs(t, err, entity.ErrNotFound)
}
