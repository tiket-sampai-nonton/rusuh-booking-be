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

	// The allocation id comes from claim.lua itself: no second GetEntry (it could race a concurrent change).
	store.EXPECT().Claim(ctx, "ev1", "e1", "v1", gomock.Any()).Return(outbound.ClaimResult{Status: "CLAIMED", AllocationID: "alloc-1"}, nil)
	tokens.EXPECT().IssueAdmission("e1", "ev1", "alloc-1", fixedNow.T).Return("adm-token", fixedNow.T.Add(time.Minute), nil)

	uc := usecase.NewClaimTurn(store, tokens, fixedNow, "https://app/book")
	resp, err := uc.Claim(ctx, inbound.ClaimInput{EntryID: "e1", VisitorID: "v1", EventID: "ev1"})
	require.NoError(t, err)
	assert.Equal(t, "adm-token", resp.AdmissionToken)
	assert.Equal(t, "https://app/book/e1", resp.BookingURL)
}

// A claim that Valkey accepted but that carries no allocation id must not mint an admission token: the token's JTI
// is the Booking primary key, so an empty one would be unusable and mask a real inconsistency.
func TestClaim_EmptyAllocationIDIsDependencyUnavailableAndSignsNothing(t *testing.T) {
	ctrl := gomock.NewController(t)
	store := mock_outbound.NewMockIQueueStore(ctrl)
	tokens := mock_outbound.NewMockITokenIssuer(ctrl) // IssueAdmission must never be called
	ctx := context.Background()
	store.EXPECT().Claim(ctx, "ev1", "e1", "v1", gomock.Any()).Return(outbound.ClaimResult{Status: "CLAIMED"}, nil)

	uc := usecase.NewClaimTurn(store, tokens, fixedNow, "https://app/book")
	_, err := uc.Claim(ctx, inbound.ClaimInput{EntryID: "e1", VisitorID: "v1", EventID: "ev1"})
	assert.ErrorIs(t, err, entity.ErrDependencyUnavailable)
}

func TestClaim_StoreErrorsAreReturned(t *testing.T) {
	for name, storeErr := range map[string]error{
		"turn missed":  entity.ErrTurnMissed,
		"not admitted": entity.ErrNotAdmitted,
		"not found":    entity.ErrNotFound,
		"valkey down":  errors.New("valkey: down"),
	} {
		t.Run(name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			store := mock_outbound.NewMockIQueueStore(ctrl)
			tokens := mock_outbound.NewMockITokenIssuer(ctrl)
			ctx := context.Background()
			store.EXPECT().Claim(ctx, "ev1", "e1", "v1", gomock.Any()).Return(outbound.ClaimResult{}, storeErr)
			uc := usecase.NewClaimTurn(store, tokens, fixedNow, "https://app/book")
			_, err := uc.Claim(ctx, inbound.ClaimInput{EntryID: "e1", VisitorID: "v1", EventID: "ev1"})
			assert.ErrorIs(t, err, storeErr)
		})
	}
}

// --- Leave (T11): ownership and status are decided by leave.lua BEFORE Booking is touched ---

func TestLeave_WaitingDoesNotCallBooking(t *testing.T) {
	ctrl := gomock.NewController(t)
	store := mock_outbound.NewMockIQueueStore(ctrl)
	booking := mock_outbound.NewMockIBookingClient(ctrl) // any call fails the test
	ctx := context.Background()
	store.EXPECT().Leave(ctx, "ev1", "e1", "v1", gomock.Any()).Return(outbound.LeaveResult{Tag: "OK", PrevStatus: "WAITING"}, nil)

	uc := usecase.NewLeaveQueue(store, booking, fixedNow)
	require.NoError(t, uc.Leave(ctx, inbound.LeaveInput{EntryID: "e1", VisitorID: "v1", EventID: "ev1"}))
}

func TestLeave_AdmittedReleasesTheAllocationAfterTheCommit(t *testing.T) {
	ctrl := gomock.NewController(t)
	store := mock_outbound.NewMockIQueueStore(ctrl)
	booking := mock_outbound.NewMockIBookingClient(ctrl)
	ctx := context.Background()
	gomock.InOrder(
		store.EXPECT().Leave(ctx, "ev1", "e1", "v1", gomock.Any()).Return(outbound.LeaveResult{Tag: "OK", PrevStatus: "ADMITTED", AllocationID: "alloc-42"}, nil),
		booking.EXPECT().ReleaseAllocation(ctx, "alloc-42", "QUEUE_ENTRY_GONE").Return(nil),
	)
	uc := usecase.NewLeaveQueue(store, booking, fixedNow)
	require.NoError(t, uc.Leave(ctx, inbound.LeaveInput{EntryID: "e1", VisitorID: "v1", EventID: "ev1"}))
}

func TestLeave_AdmittedReleaseFailureDoesNotFailTheRequest(t *testing.T) {
	ctrl := gomock.NewController(t)
	store := mock_outbound.NewMockIQueueStore(ctrl)
	booking := mock_outbound.NewMockIBookingClient(ctrl)
	ctx := context.Background()
	store.EXPECT().Leave(ctx, "ev1", "e1", "v1", gomock.Any()).Return(outbound.LeaveResult{Tag: "OK", PrevStatus: "ADMITTED", AllocationID: "alloc-42"}, nil)
	booking.EXPECT().ReleaseAllocation(ctx, "alloc-42", "QUEUE_ENTRY_GONE").Return(errors.New("booking down"))

	uc := usecase.NewLeaveQueue(store, booking, fixedNow)
	require.NoError(t, uc.Leave(ctx, inbound.LeaveInput{EntryID: "e1", VisitorID: "v1", EventID: "ev1"}))
}

// Security regression: a visitor who is not the owner must never cause a Booking release. Before the fix the
// usecase read the entry first and released its allocation BEFORE leave.lua rejected the stranger, so anyone who
// knew an entry id could free another buyer's reserved ticket.
func TestLeave_StrangerCausesNoBookingCall(t *testing.T) {
	ctrl := gomock.NewController(t)
	store := mock_outbound.NewMockIQueueStore(ctrl)
	booking := mock_outbound.NewMockIBookingClient(ctrl) // any call fails the test
	ctx := context.Background()
	store.EXPECT().Leave(ctx, "ev1", "e1", "intruder", gomock.Any()).Return(outbound.LeaveResult{}, entity.ErrNotFound)

	uc := usecase.NewLeaveQueue(store, booking, fixedNow)
	err := uc.Leave(ctx, inbound.LeaveInput{EntryID: "e1", VisitorID: "intruder", EventID: "ev1"})
	assert.ErrorIs(t, err, entity.ErrNotFound)
}

func TestLeave_StoreErrorMeansBookingIsNeverCalled(t *testing.T) {
	ctrl := gomock.NewController(t)
	store := mock_outbound.NewMockIQueueStore(ctrl)
	booking := mock_outbound.NewMockIBookingClient(ctrl)
	ctx := context.Background()
	store.EXPECT().Leave(ctx, "ev1", "e1", "v1", gomock.Any()).Return(outbound.LeaveResult{}, errors.New("valkey: down"))
	uc := usecase.NewLeaveQueue(store, booking, fixedNow)
	require.Error(t, uc.Leave(ctx, inbound.LeaveInput{EntryID: "e1", VisitorID: "v1", EventID: "ev1"}))
}

// TC-QUE-23: terminal and CLAIMED entries answer 204 with zero releases.
func TestLeave_IgnoredStatusesReleaseNothing(t *testing.T) {
	for _, st := range []string{"COMPLETED", "ENDED", "LEFT", "MISSED", "CLAIMED"} {
		t.Run(st, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			store := mock_outbound.NewMockIQueueStore(ctrl)
			booking := mock_outbound.NewMockIBookingClient(ctrl)
			ctx := context.Background()
			store.EXPECT().Leave(ctx, "ev1", "e1", "v1", gomock.Any()).Return(outbound.LeaveResult{Tag: "IGNORED", PrevStatus: st, AllocationID: "alloc-42"}, nil)
			uc := usecase.NewLeaveQueue(store, booking, fixedNow)
			require.NoError(t, uc.Leave(ctx, inbound.LeaveInput{EntryID: "e1", VisitorID: "v1", EventID: "ev1"}))
		})
	}
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
