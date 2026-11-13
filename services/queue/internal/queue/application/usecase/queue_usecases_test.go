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
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/port/inbound"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/port/outbound"
)

var fixedNow = clock.Fixed{T: time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC)}

func TestClaim_Success(t *testing.T) {
	ctrl := gomock.NewController(t)
	store := mock_outbound.NewMockIQueueStore(ctrl)
	store.EXPECT().CheckTokenGen(gomock.Any(), "ev1", gomock.Any(), gomock.Any()).Return(outbound.TokenGenOK, nil).AnyTimes()
	tokens := mock_outbound.NewMockITokenIssuer(ctrl)
	ctx := context.Background()

	// The allocation id comes from claim.lua itself: no second GetEntry (it could race a concurrent change).
	store.EXPECT().Claim(ctx, "ev1", "e1", "v1", gomock.Any()).Return(outbound.ClaimResult{Status: "CLAIMED", AllocationID: "alloc-1"}, nil)
	tokens.EXPECT().IssueAdmission("e1", "ev1", "alloc-1", fixedNow.T).Return("adm-token", fixedNow.T.Add(time.Minute), nil)

	uc := usecase.NewClaimTurn(store, tokens, fixedNow, "https://app/book")
	resp, err := uc.Claim(ctx, inbound.ClaimInput{EntryID: "e1", VisitorID: "v1", EventID: "ev1", Gen: 1})
	require.NoError(t, err)
	assert.Equal(t, "adm-token", resp.AdmissionToken)
	assert.Equal(t, "https://app/book/ev1", resp.BookingURL, "page URL is base/eventId, not entryId")
}

// A claim that Valkey accepted but that carries no allocation id must not mint an admission token: the token's JTI
// is the Booking primary key, so an empty one would be unusable and mask a real inconsistency.
func TestClaim_EmptyAllocationIDIsDependencyUnavailableAndSignsNothing(t *testing.T) {
	ctrl := gomock.NewController(t)
	store := mock_outbound.NewMockIQueueStore(ctrl)
	store.EXPECT().CheckTokenGen(gomock.Any(), "ev1", gomock.Any(), gomock.Any()).Return(outbound.TokenGenOK, nil).AnyTimes()
	tokens := mock_outbound.NewMockITokenIssuer(ctrl) // IssueAdmission must never be called
	ctx := context.Background()
	store.EXPECT().Claim(ctx, "ev1", "e1", "v1", gomock.Any()).Return(outbound.ClaimResult{Status: "CLAIMED"}, nil)

	uc := usecase.NewClaimTurn(store, tokens, fixedNow, "https://app/book")
	_, err := uc.Claim(ctx, inbound.ClaimInput{EntryID: "e1", VisitorID: "v1", EventID: "ev1", Gen: 1})
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
			store.EXPECT().CheckTokenGen(gomock.Any(), "ev1", gomock.Any(), gomock.Any()).Return(outbound.TokenGenOK, nil).AnyTimes()
			tokens := mock_outbound.NewMockITokenIssuer(ctrl)
			ctx := context.Background()
			store.EXPECT().Claim(ctx, "ev1", "e1", "v1", gomock.Any()).Return(outbound.ClaimResult{}, storeErr)
			uc := usecase.NewClaimTurn(store, tokens, fixedNow, "https://app/book")
			resp, err := uc.Claim(ctx, inbound.ClaimInput{EntryID: "e1", VisitorID: "v1", EventID: "ev1", Gen: 1})
			assert.ErrorIs(t, err, storeErr)
			assert.Equal(t, dto.ClaimResponse{}, resp, "no token and no URL on failure")
		})
	}
}

// --- Leave (T11): ownership and status are decided by leave.lua BEFORE Booking is touched ---

func TestLeave_WaitingDoesNotCallBooking(t *testing.T) {
	ctrl := gomock.NewController(t)
	store := mock_outbound.NewMockIQueueStore(ctrl)
	store.EXPECT().CheckTokenGen(gomock.Any(), "ev1", gomock.Any(), gomock.Any()).Return(outbound.TokenGenOK, nil).AnyTimes()
	booking := mock_outbound.NewMockIBookingClient(ctrl) // any call fails the test
	ctx := context.Background()
	store.EXPECT().Leave(ctx, "ev1", "e1", "v1", gomock.Any()).Return(outbound.LeaveResult{Tag: "OK", PrevStatus: "WAITING"}, nil)

	uc := usecase.NewLeaveQueue(store, booking, fixedNow)
	require.NoError(t, uc.Leave(ctx, inbound.LeaveInput{EntryID: "e1", VisitorID: "v1", EventID: "ev1", Gen: 1}))
}

func TestLeave_AdmittedReleasesTheAllocationAfterTheCommit(t *testing.T) {
	ctrl := gomock.NewController(t)
	store := mock_outbound.NewMockIQueueStore(ctrl)
	store.EXPECT().CheckTokenGen(gomock.Any(), "ev1", gomock.Any(), gomock.Any()).Return(outbound.TokenGenOK, nil).AnyTimes()
	booking := mock_outbound.NewMockIBookingClient(ctrl)
	ctx := context.Background()
	gomock.InOrder(
		store.EXPECT().Leave(ctx, "ev1", "e1", "v1", gomock.Any()).Return(outbound.LeaveResult{Tag: "OK", PrevStatus: "ADMITTED", AllocationID: "alloc-42"}, nil),
		booking.EXPECT().ReleaseAllocation(ctx, "alloc-42", "QUEUE_ENTRY_GONE").Return(nil),
	)
	uc := usecase.NewLeaveQueue(store, booking, fixedNow)
	require.NoError(t, uc.Leave(ctx, inbound.LeaveInput{EntryID: "e1", VisitorID: "v1", EventID: "ev1", Gen: 1}))
}

func TestLeave_AdmittedReleaseFailureDoesNotFailTheRequest(t *testing.T) {
	ctrl := gomock.NewController(t)
	store := mock_outbound.NewMockIQueueStore(ctrl)
	store.EXPECT().CheckTokenGen(gomock.Any(), "ev1", gomock.Any(), gomock.Any()).Return(outbound.TokenGenOK, nil).AnyTimes()
	booking := mock_outbound.NewMockIBookingClient(ctrl)
	ctx := context.Background()
	store.EXPECT().Leave(ctx, "ev1", "e1", "v1", gomock.Any()).Return(outbound.LeaveResult{Tag: "OK", PrevStatus: "ADMITTED", AllocationID: "alloc-42"}, nil)
	booking.EXPECT().ReleaseAllocation(ctx, "alloc-42", "QUEUE_ENTRY_GONE").Return(errors.New("booking down"))

	uc := usecase.NewLeaveQueue(store, booking, fixedNow)
	require.NoError(t, uc.Leave(ctx, inbound.LeaveInput{EntryID: "e1", VisitorID: "v1", EventID: "ev1", Gen: 1}))
}

// Security regression: a visitor who is not the owner must never cause a Booking release. Before the fix the
// usecase read the entry first and released its allocation BEFORE leave.lua rejected the stranger, so anyone who
// knew an entry id could free another buyer's reserved ticket.
func TestLeave_StrangerCausesNoBookingCall(t *testing.T) {
	ctrl := gomock.NewController(t)
	store := mock_outbound.NewMockIQueueStore(ctrl)
	store.EXPECT().CheckTokenGen(gomock.Any(), "ev1", gomock.Any(), gomock.Any()).Return(outbound.TokenGenOK, nil).AnyTimes()
	booking := mock_outbound.NewMockIBookingClient(ctrl) // any call fails the test
	ctx := context.Background()
	store.EXPECT().Leave(ctx, "ev1", "e1", "intruder", gomock.Any()).Return(outbound.LeaveResult{}, entity.ErrNotFound)

	uc := usecase.NewLeaveQueue(store, booking, fixedNow)
	err := uc.Leave(ctx, inbound.LeaveInput{EntryID: "e1", VisitorID: "intruder", EventID: "ev1", Gen: 1})
	assert.ErrorIs(t, err, entity.ErrNotFound)
}

func TestLeave_StoreErrorMeansBookingIsNeverCalled(t *testing.T) {
	ctrl := gomock.NewController(t)
	store := mock_outbound.NewMockIQueueStore(ctrl)
	store.EXPECT().CheckTokenGen(gomock.Any(), "ev1", gomock.Any(), gomock.Any()).Return(outbound.TokenGenOK, nil).AnyTimes()
	booking := mock_outbound.NewMockIBookingClient(ctrl)
	ctx := context.Background()
	store.EXPECT().Leave(ctx, "ev1", "e1", "v1", gomock.Any()).Return(outbound.LeaveResult{}, errors.New("valkey: down"))
	uc := usecase.NewLeaveQueue(store, booking, fixedNow)
	require.Error(t, uc.Leave(ctx, inbound.LeaveInput{EntryID: "e1", VisitorID: "v1", EventID: "ev1", Gen: 1}))
}

// TC-QUE-23: terminal entries answer 204 with zero releases. (CLAIMED is refused with ErrEntryClaimed: TC-QUE-38.)
func TestLeave_IgnoredStatusesReleaseNothing(t *testing.T) {
	for _, st := range []string{"COMPLETED", "ENDED", "LEFT", "MISSED"} {
		t.Run(st, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			store := mock_outbound.NewMockIQueueStore(ctrl)
			store.EXPECT().CheckTokenGen(gomock.Any(), "ev1", gomock.Any(), gomock.Any()).Return(outbound.TokenGenOK, nil).AnyTimes()
			booking := mock_outbound.NewMockIBookingClient(ctrl)
			ctx := context.Background()
			store.EXPECT().Leave(ctx, "ev1", "e1", "v1", gomock.Any()).Return(outbound.LeaveResult{Tag: "IGNORED", PrevStatus: st, AllocationID: "alloc-42"}, nil)
			uc := usecase.NewLeaveQueue(store, booking, fixedNow)
			require.NoError(t, uc.Leave(ctx, inbound.LeaveInput{EntryID: "e1", VisitorID: "v1", EventID: "ev1", Gen: 1}))
		})
	}
}

func TestGetStatus_Found(t *testing.T) {
	ctrl := gomock.NewController(t)
	store := mock_outbound.NewMockIQueueStore(ctrl)
	ctx := context.Background()
	doc := dto.StatusDocument{EventID: "ev1", SaleState: "OPEN", SoldOut: false}
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

// A rotated-out visitor token (generation no longer current) can neither claim nor leave: nothing reaches the store
// or Booking (ADR-030 R01.4; a stale device must not claim an admission that holds 3 units).
func TestClaimAndLeave_RotatedOutTokenIsRefused(t *testing.T) {
	for name, gen := range map[string]int64{"older generation": 1, "no generation": 0} {
		t.Run(name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			store := mock_outbound.NewMockIQueueStore(ctrl) // Claim/Leave must never be called
			tokens := mock_outbound.NewMockITokenIssuer(ctrl)
			booking := mock_outbound.NewMockIBookingClient(ctrl)
			ctx := context.Background()
			store.EXPECT().CheckTokenGen(ctx, "ev1", "v1", gomock.Any()).Return(outbound.TokenGenInvalid, nil).Times(2)

			_, err := usecase.NewClaimTurn(store, tokens, fixedNow, "https://app/book").
				Claim(ctx, inbound.ClaimInput{EntryID: "e1", VisitorID: "v1", EventID: "ev1", Gen: gen})
			assert.ErrorIs(t, err, entity.ErrVisitorTokenInvalid)
			err = usecase.NewLeaveQueue(store, booking, fixedNow).
				Leave(ctx, inbound.LeaveInput{EntryID: "e1", VisitorID: "v1", EventID: "ev1", Gen: gen})
			assert.ErrorIs(t, err, entity.ErrVisitorTokenInvalid)
		})
	}
}

// TC-QUE-35 / TC-SEC-01: the booking page URL is base + "/" + escaped eventId and carries no token or internal host.
func TestClaim_BookingURLShape(t *testing.T) {
	tests := []struct {
		name, base, eventID, want string
	}{
		{"plain", "https://app.example/book", "5b1f-uuid", "https://app.example/book/5b1f-uuid"},
		{"trailing slash base", "https://app.example/book/", "ev1", "https://app.example/book/ev1"},
		{"slash in id", "https://app.example/book", "a/b", "https://app.example/book/a%2Fb"},
		{"query chars", "https://app.example/book", "a?x=1", "https://app.example/book/a%3Fx=1"},
		{"fragment", "https://app.example/book", "a#f", "https://app.example/book/a%23f"},
		{"space", "https://app.example/book", "a b", "https://app.example/book/a%20b"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			store := mock_outbound.NewMockIQueueStore(ctrl)
			store.EXPECT().CheckTokenGen(gomock.Any(), tt.eventID, gomock.Any(), gomock.Any()).Return(outbound.TokenGenOK, nil).AnyTimes()
			tokens := mock_outbound.NewMockITokenIssuer(ctrl)
			ctx := context.Background()
			store.EXPECT().Claim(ctx, tt.eventID, "entry-secret-id", "visitor-secret-id", gomock.Any()).
				Return(outbound.ClaimResult{Status: "CLAIMED", AllocationID: "alloc-1"}, nil)
			tokens.EXPECT().IssueAdmission("entry-secret-id", tt.eventID, "alloc-1", fixedNow.T).
				Return("distinctive.admission.token", fixedNow.T.Add(time.Minute), nil)

			resp, err := usecase.NewClaimTurn(store, tokens, fixedNow, tt.base).
				Claim(ctx, inbound.ClaimInput{EntryID: "entry-secret-id", VisitorID: "visitor-secret-id", EventID: tt.eventID, Gen: 1})
			require.NoError(t, err)
			assert.Equal(t, tt.want, resp.BookingURL)
			assert.Equal(t, "distinctive.admission.token", resp.AdmissionToken)
			for _, leak := range []string{"distinctive.admission.token", "entry-secret-id", "visitor-secret-id", "booking:8082"} {
				assert.NotContains(t, resp.BookingURL, leak)
			}
			assert.NotContains(t, resp.BookingURL, "?")
			assert.NotContains(t, resp.BookingURL, "#")
		})
	}
}
