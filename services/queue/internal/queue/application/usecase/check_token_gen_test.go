package usecase_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	mock_outbound "github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/_mock/queue/port/outbound"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/application/usecase"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/domain/entity"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/port/inbound"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/port/outbound"
)

// TC-QUE-42/43 (REQ-QVT-02/03, P4-P6): the token-generation check shared by claim, leave and me maps the store's
// outcome three ways (D-C6.1: the store decides, the usecase maps): dependency error -> as is; empty eventId -> 401;
// no generation record (never joined this event) -> 404; invalid (older or non-positive generation) -> 401; OK -> proceeds.
// It runs before any state change.
func TestTokenGenCheck_ThreeWay(t *testing.T) {
	tests := []struct {
		name    string
		eventID string
		gen     int64
		res     outbound.TokenGenCheck // what the store reports for the visitor's record
		stErr   error
		wantErr error // nil = the check passes and the usecase proceeds past it
		noStore bool  // CheckTokenGen must not even be consulted
	}{
		{name: "store error is returned as is", eventID: "ev1", gen: 1, stErr: errors.New("boom"), wantErr: errors.New("boom")},
		{name: "empty eventId stays 401", eventID: "", gen: 1, wantErr: entity.ErrVisitorTokenInvalid, noStore: true},
		{name: "no record -> never joined -> 404", eventID: "ev1", gen: 1, res: outbound.TokenGenAbsent, wantErr: entity.ErrNotFound},
		{name: "legacy gen 0 -> 401", eventID: "ev1", gen: 0, res: outbound.TokenGenInvalid, wantErr: entity.ErrVisitorTokenInvalid},
		{name: "rotated (older generation) -> 401", eventID: "ev1", gen: 1, res: outbound.TokenGenInvalid, wantErr: entity.ErrVisitorTokenInvalid},
		{name: "newer than stored is ratcheted by the store -> passes", eventID: "ev1", gen: 5, res: outbound.TokenGenOK},
		{name: "current generation passes", eventID: "ev1", gen: 2, res: outbound.TokenGenOK},
	}
	type runner struct {
		name string
		run  func(t *testing.T, store *mock_outbound.MockIQueueStore, ctrl *gomock.Controller, ev string, gen int64) error
	}
	runners := []runner{
		{"claim", func(t *testing.T, store *mock_outbound.MockIQueueStore, ctrl *gomock.Controller, ev string, gen int64) error {
			_, err := usecase.NewClaimTurn(store, mock_outbound.NewMockITokenIssuer(ctrl), fixedNow, "https://app/book", 5700*time.Second).
				Claim(context.Background(), inbound.ClaimInput{EntryID: "e1", VisitorID: "v1", EventID: ev, Gen: gen})
			return err
		}},
		{"leave", func(t *testing.T, store *mock_outbound.MockIQueueStore, ctrl *gomock.Controller, ev string, gen int64) error {
			return usecase.NewLeaveQueue(store, mock_outbound.NewMockIBookingClient(ctrl), fixedNow).
				Leave(context.Background(), inbound.LeaveInput{EntryID: "e1", VisitorID: "v1", EventID: ev, Gen: gen})
		}},
		{"me", func(t *testing.T, store *mock_outbound.MockIQueueStore, ctrl *gomock.Controller, ev string, gen int64) error {
			_, err := usecase.NewGetMyEntry(store).GetMyEntry(context.Background(), inbound.MyEntryInput{EventID: ev, VisitorID: "v1", Gen: gen})
			return err
		}},
	}
	for _, r := range runners {
		for _, tt := range tests {
			t.Run(r.name+"/"+tt.name, func(t *testing.T) {
				ctrl := gomock.NewController(t)
				// Strict mock: anything not expected below (Claim, Leave, FindByVisitor, ReleaseAllocation...) fails the test.
				store := mock_outbound.NewMockIQueueStore(ctrl)
				if !tt.noStore {
					store.EXPECT().CheckTokenGen(gomock.Any(), tt.eventID, "v1", tt.gen).Return(tt.res, tt.stErr)
				}
				if tt.wantErr == nil {
					// The check passed: the usecase moves on to its own store call; stop it with a sentinel.
					sentinel := errors.New("past-the-check")
					switch r.name {
					case "claim":
						store.EXPECT().Claim(gomock.Any(), tt.eventID, "e1", "v1", gomock.Any(), gomock.Any()).Return(outbound.ClaimResult{}, sentinel)
					case "leave":
						store.EXPECT().Leave(gomock.Any(), tt.eventID, "e1", "v1", gomock.Any()).Return(outbound.LeaveResult{}, sentinel)
					case "me":
						store.EXPECT().FindByVisitor(gomock.Any(), tt.eventID, "v1").Return(entity.Entry{}, false, sentinel)
					}
					err := r.run(t, store, ctrl, tt.eventID, tt.gen)
					assert.EqualError(t, err, sentinel.Error())
					return
				}
				err := r.run(t, store, ctrl, tt.eventID, tt.gen)
				require.Error(t, err)
				if errors.Is(tt.wantErr, entity.ErrNotFound) || errors.Is(tt.wantErr, entity.ErrVisitorTokenInvalid) {
					assert.ErrorIs(t, err, tt.wantErr)
				} else {
					assert.EqualError(t, err, tt.wantErr.Error())
				}
			})
		}
	}
}

// D4 (P4, NFR-07): a rotated token is refused with 401 for an own, a foreign and a nonexistent entry id alike, and
// nothing is claimed, left or released (a stale token must not free another buyer's allocation).
func TestClaimAndLeave_RotatedTokenIsIdenticalForOwnForeignAndAbsentEntries(t *testing.T) {
	for _, entryID := range []string{"own-entry", "foreign-entry", "does-not-exist"} {
		t.Run(entryID, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			store := mock_outbound.NewMockIQueueStore(ctrl)
			booking := mock_outbound.NewMockIBookingClient(ctrl)
			tokens := mock_outbound.NewMockITokenIssuer(ctrl)
			store.EXPECT().CheckTokenGen(gomock.Any(), "ev1", "v1", gomock.Any()).Return(outbound.TokenGenInvalid, nil).Times(2)
			store.EXPECT().Claim(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Times(0)
			store.EXPECT().Leave(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Times(0)
			booking.EXPECT().ReleaseAllocation(gomock.Any(), gomock.Any(), gomock.Any()).Times(0)

			_, cerr := usecase.NewClaimTurn(store, tokens, fixedNow, "https://app/book", 5700*time.Second).
				Claim(context.Background(), inbound.ClaimInput{EntryID: entryID, VisitorID: "v1", EventID: "ev1", Gen: 2})
			lerr := usecase.NewLeaveQueue(store, booking, fixedNow).
				Leave(context.Background(), inbound.LeaveInput{EntryID: entryID, VisitorID: "v1", EventID: "ev1", Gen: 2})
			assert.Equal(t, 401, inbound.MapError(cerr).HTTPStatus)
			assert.Equal(t, 401, inbound.MapError(lerr).HTTPStatus)
			assert.Equal(t, "VISITOR_TOKEN_INVALID", string(inbound.MapError(cerr).Code))
			assert.Equal(t, "VISITOR_TOKEN_INVALID", string(inbound.MapError(lerr).Code))
		})
	}
}

// D6/D7 (P5): with a valid unrotated token, a foreign and an absent entry produce byte-identical 404 mappings.
func TestClaimAndLeave_ForeignAndAbsentEntryAreIndistinguishable(t *testing.T) {
	ctrl := gomock.NewController(t)
	store := mock_outbound.NewMockIQueueStore(ctrl)
	store.EXPECT().CheckTokenGen(gomock.Any(), "ev1", "v1", gomock.Any()).Return(outbound.TokenGenOK, nil).AnyTimes()
	store.EXPECT().Claim(gomock.Any(), "ev1", gomock.Any(), "v1", gomock.Any(), gomock.Any()).Return(outbound.ClaimResult{}, entity.ErrNotFound).Times(2)
	store.EXPECT().Leave(gomock.Any(), "ev1", gomock.Any(), "v1", gomock.Any()).Return(outbound.LeaveResult{}, entity.ErrNotFound).Times(2)
	tokens := mock_outbound.NewMockITokenIssuer(ctrl)
	booking := mock_outbound.NewMockIBookingClient(ctrl)
	claim := usecase.NewClaimTurn(store, tokens, fixedNow, "https://app/book", 5700*time.Second)
	leave := usecase.NewLeaveQueue(store, booking, fixedNow)

	var got []string
	for _, id := range []string{"foreign-entry", "absent-entry"} {
		_, cerr := claim.Claim(context.Background(), inbound.ClaimInput{EntryID: id, VisitorID: "v1", EventID: "ev1", Gen: 1})
		lerr := leave.Leave(context.Background(), inbound.LeaveInput{EntryID: id, VisitorID: "v1", EventID: "ev1", Gen: 1})
		for _, err := range []error{cerr, lerr} {
			ae := inbound.MapError(err)
			got = append(got, ae.Error())
			assert.Equal(t, 404, ae.HTTPStatus)
		}
	}
	assert.Equal(t, got[0], got[2])
	assert.Equal(t, got[1], got[3])
}

// D-C6.1 (AC-C6.1 P0-8): `me` maps the store outcome directly: an absent record is 404, an invalid generation is 401,
// and a valid one reads the entry (the usecase's own store call, stopped here by a sentinel).
func TestGetMyEntry_TokenGenOutcomes(t *testing.T) {
	sentinel := errors.New("past-the-check")
	tests := []struct {
		name    string
		res     outbound.TokenGenCheck
		wantErr error
	}{
		{name: "absent -> ErrNotFound", res: outbound.TokenGenAbsent, wantErr: entity.ErrNotFound},
		{name: "invalid -> ErrVisitorTokenInvalid", res: outbound.TokenGenInvalid, wantErr: entity.ErrVisitorTokenInvalid},
		{name: "ok -> reads the entry", res: outbound.TokenGenOK, wantErr: sentinel},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			store := mock_outbound.NewMockIQueueStore(ctrl)
			store.EXPECT().CheckTokenGen(gomock.Any(), "ev1", "v1", int64(2)).Return(tt.res, nil)
			if tt.res == outbound.TokenGenOK {
				store.EXPECT().FindByVisitor(gomock.Any(), "ev1", "v1").Return(entity.Entry{}, false, sentinel)
			}
			_, err := usecase.NewGetMyEntry(store).GetMyEntry(context.Background(), inbound.MyEntryInput{EventID: "ev1", VisitorID: "v1", Gen: 2})
			require.ErrorIs(t, err, tt.wantErr)
		})
	}
}
