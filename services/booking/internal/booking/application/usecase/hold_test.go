package usecase_test

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"regexp"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/application/usecase"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/dto"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/entity"
	vo "github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/valueobject"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/inbound"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/outbound"
)

const (
	sessionID = "11111111-1111-1111-1111-111111111111"
	eventID   = "22222222-2222-2222-2222-222222222222"
	catA      = "33333333-3333-3333-3333-333333333333"
	catB      = "44444444-4444-4444-4444-444444444444"
	visitor   = "55555555-5555-5555-5555-555555555555"
	idemKey   = "idem-test-hold-01"
	nik1      = "3174010101010001"
	nik2      = "3174010101010002"
)

func activeAdmission(mixed bool) entity.Admission {
	return entity.Admission{
		ID: sessionID, EventID: eventID, VisitorID: visitor, Status: vo.AdmissionActive,
		ExpiresAt: time.Now().Add(time.Hour), Units: 3, CfgPaymentTTLSeconds: 600,
		CfgEditEnabled: true, CfgMaxEdits: 3, CfgAllowMixedCategories: mixed,
	}
}

func detailWith(mixed bool, maxOrder int) entity.EventDetail {
	return entity.EventDetail{
		Event: entity.Event{ID: eventID, Published: true, MaxTicketsPerOrder: maxOrder, AllowMixedCategories: mixed},
		Categories: []entity.CategoryDetail{
			{Category: entity.Category{ID: catA, Code: "A", PriceMinor: 1000}, Available: 50},
			{Category: entity.Category{ID: catB, Code: "B", PriceMinor: 2000}, Available: 50},
		},
	}
}

const (
	buyerNIK = "3174010101010009"
	email    = "buyer@example.com"
)

var testPolicy = usecase.HoldPolicy{RefusalBudget: 3, ThrottleBase: 2 * time.Second, ThrottleMax: 30 * time.Second}

func newHoldUC(adm *fakeAdmissionRepo, ev *fakeEventRepo, idem *fakeIdem, uow *fakeUoW) *usecase.Hold {
	return usecase.NewHold(adm, ev, idem, uow, fakeNIK{}, &fakeIDs{id: "bk-1", ref: "RB-1"}, fixedClock{t: time.Now()}, testPolicy)
}

func buyer() dto.BuyerInput {
	return dto.BuyerInput{NIK: buyerNIK, Email: email, EmailConfirmation: email, Consent: true}
}

func holdCmd(items []dto.HoldItem) inbound.HoldCommand {
	return inbound.HoldCommand{
		Request:        dto.HoldRequest{SessionID: sessionID, Buyer: buyer(), Items: items},
		VisitorID:      visitor,
		IdempotencyKey: idemKey,
	}
}

func oneItem() []dto.HoldItem {
	return []dto.HoldItem{{CategoryID: catA, Attendee: dto.AttendeeInput{NIK: nik1, FullName: "Alice"}}}
}

func admission3() entity.Admission {
	a := activeAdmission(true)
	a.OutstandingUnits = 3
	return a
}

func TestHold_Success(t *testing.T) {
	uow := &fakeUoW{holdRes: outbound.HoldResult{BookingID: "bk-1", PublicRef: "RB-1", Version: 1, Status: "PENDING_PAYMENT", AmountMinor: 3000, PaymentExpiresAt: time.Now().Add(time.Hour), ItemIDs: []string{"77777777-7777-7777-7777-777777777777", "88888888-8888-8888-8888-888888888888"}}}
	idem := &fakeIdem{}
	uc := newHoldUC(&fakeAdmissionRepo{admission: admission3()}, &fakeEventRepo{detail: detailWith(true, 3)}, idem, uow)

	resp, err := uc.Hold(context.Background(), holdCmd([]dto.HoldItem{
		{CategoryID: catA, Attendee: dto.AttendeeInput{NIK: nik1, FullName: "Alice"}},
		{CategoryID: catB, Attendee: dto.AttendeeInput{NIK: nik2, FullName: "Bob"}},
	}))
	require.NoError(t, err)
	assert.Equal(t, "bk-1", resp.BookingID)
	assert.EqualValues(t, 3000, resp.AmountMinor)
	assert.Equal(t, 2, len(uow.heldSpec.Lines))
	assert.True(t, idem.completed, "idempotency record completed")
	assert.Equal(t, "RB-1", resp.OrderID)
	assert.Equal(t, 2, resp.TicketQuantity)
	// The buyer data comes from the request, not from the admission (ADR-030).
	assert.Equal(t, "3174********0009", resp.BuyerNIKMasked)
	assert.Equal(t, []byte("h:"+buyerNIK), uow.heldSpec.BuyerNIKHash)
	assert.Equal(t, "sealed:"+email, uow.heldSpec.BuyerEmailEnc)
	assert.Equal(t, 3, uow.heldSpec.RefusalBudget)
}

func TestHold_ValidationOrderAndNoIO(t *testing.T) {
	tests := []struct {
		name  string
		edit  func(*dto.HoldRequest)
		wants error
	}{
		{"empty items", func(r *dto.HoldRequest) { r.Items = nil }, inbound.ErrQuantityInvalid},
		{"bad buyer nik", func(r *dto.HoldRequest) { r.Buyer.NIK = "123" }, inbound.ErrNIKInvalid},
		{"email mismatch", func(r *dto.HoldRequest) { r.Buyer.EmailConfirmation = "other@example.com" }, inbound.ErrEmailMismatch},
		{"consent missing", func(r *dto.HoldRequest) { r.Buyer.Consent = false }, inbound.ErrConsentRequired},
		{"mismatch wins over consent", func(r *dto.HoldRequest) {
			r.Buyer.Consent = false
			r.Buyer.EmailConfirmation = "other@example.com"
		}, inbound.ErrEmailMismatch},
		{"invalid buyer nik wins over mismatch", func(r *dto.HoldRequest) {
			r.Buyer.NIK = "x"
			r.Buyer.EmailConfirmation = "other@example.com"
		}, inbound.ErrNIKInvalid},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			adm := &fakeAdmissionRepo{admission: admission3()}
			idem := &fakeIdem{}
			uc := newHoldUC(adm, &fakeEventRepo{detail: detailWith(true, 3)}, idem, &fakeUoW{})
			cmd := holdCmd(oneItem())
			tt.edit(&cmd.Request)
			_, err := uc.Hold(context.Background(), cmd)
			assert.ErrorIs(t, err, tt.wants)
			assert.Zero(t, adm.getCalls, "pure validation: no admission read")
			assert.Nil(t, idem.gotHash, "malformed request never creates an idempotency record")
		})
	}
	t.Run("invalid email format", func(t *testing.T) {
		uc := newHoldUC(&fakeAdmissionRepo{admission: admission3()}, &fakeEventRepo{detail: detailWith(true, 3)}, &fakeIdem{}, &fakeUoW{})
		cmd := holdCmd(oneItem())
		cmd.Request.Buyer.Email, cmd.Request.Buyer.EmailConfirmation = "not-an-email", "not-an-email"
		_, err := uc.Hold(context.Background(), cmd)
		require.Error(t, err)
	})
}

// REQ-3: the hold response carries a real, DB-generated booking_items.id per item, in request-line order
// (per-index correspondence), and the item count equals the number of lines.
func TestHold_ResponseCarriesPerLineItemIDs(t *testing.T) {
	itemIDs := []string{
		"77777777-7777-7777-7777-777777777777",
		"88888888-8888-8888-8888-888888888888",
	}
	uow := &fakeUoW{holdRes: outbound.HoldResult{
		BookingID: "bk-1", PublicRef: "RB-1", Version: 1, Status: "PENDING_PAYMENT",
		AmountMinor: 3000, PaymentExpiresAt: time.Now().Add(time.Hour), ItemIDs: itemIDs,
	}}
	adm := activeAdmission(true)
	adm.OutstandingUnits = 2 // two reserved units granted for this 2-line hold
	uc := newHoldUC(&fakeAdmissionRepo{admission: adm}, &fakeEventRepo{detail: detailWith(true, 3)}, &fakeIdem{}, uow)

	resp, err := uc.Hold(context.Background(), holdCmd([]dto.HoldItem{
		{CategoryID: catA, Attendee: dto.AttendeeInput{NIK: nik1, FullName: "Alice"}},
		{CategoryID: catB, Attendee: dto.AttendeeInput{NIK: nik2, FullName: "Bob"}},
	}))
	require.NoError(t, err)
	require.Len(t, resp.Items, 2, "item count equals line count")
	// Per-index correspondence: Items[i].ItemID is the id generated for request line i, and each is a non-empty UUID.
	uuidPat := regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
	for i, it := range resp.Items {
		assert.Equal(t, itemIDs[i], it.ItemID, "Items[%d].ItemID must be the id for line %d", i, i)
		assert.True(t, uuidPat.MatchString(it.ItemID), "Items[%d].ItemID %q must be a UUID", i, it.ItemID)
	}
	// The category/attendee of each item still comes from the matching request line (order preserved).
	assert.Equal(t, catA, resp.Items[0].CategoryID)
	assert.Equal(t, catB, resp.Items[1].CategoryID)
}

// REQ-3 / INV-1: a would-be-oversell hold is refused with the same typed error and produces no items.
func TestHold_RefusedOnOversellReturnsNoItems(t *testing.T) {
	uow := &fakeUoW{holdErr: inbound.ErrCategoryUnavailable}
	adm := activeAdmission(true)
	adm.OutstandingUnits = 1 // one reserved unit granted; the UoW refuses it as oversell
	uc := newHoldUC(&fakeAdmissionRepo{admission: adm}, &fakeEventRepo{detail: detailWith(true, 3)}, &fakeIdem{}, uow)
	resp, err := uc.Hold(context.Background(), holdCmd([]dto.HoldItem{
		{CategoryID: catA, Attendee: dto.AttendeeInput{NIK: nik1, FullName: "Alice"}},
	}))
	assert.ErrorIs(t, err, inbound.ErrCategoryUnavailable)
	assert.Empty(t, resp.Items, "a refused hold must carry no items")
}

func TestHold_QuantityExceeds(t *testing.T) {
	uc := newHoldUC(&fakeAdmissionRepo{admission: admission3()}, &fakeEventRepo{detail: detailWith(true, 1)}, &fakeIdem{}, &fakeUoW{})
	_, err := uc.Hold(context.Background(), holdCmd([]dto.HoldItem{
		{CategoryID: catA, Attendee: dto.AttendeeInput{NIK: nik1, FullName: "Alice"}},
		{CategoryID: catB, Attendee: dto.AttendeeInput{NIK: nik2, FullName: "Bob"}},
	}))
	assert.ErrorIs(t, err, inbound.ErrQuantityExceeds)
}

func TestHold_MoreThanOutstandingUnitsIsQuantityInvalid(t *testing.T) {
	adm := admission3()
	adm.OutstandingUnits = 1 // partial grant (stock tail 3,3,1)
	idem := &fakeIdem{}
	uow := &fakeUoW{}
	uc := newHoldUC(&fakeAdmissionRepo{admission: adm}, &fakeEventRepo{detail: detailWith(true, 3)}, idem, uow)
	_, err := uc.Hold(context.Background(), holdCmd([]dto.HoldItem{
		{CategoryID: catA, Attendee: dto.AttendeeInput{NIK: nik1, FullName: "Alice"}},
		{CategoryID: catB, Attendee: dto.AttendeeInput{NIK: nik2, FullName: "Bob"}},
	}))
	assert.ErrorIs(t, err, inbound.ErrQuantityInvalid)
	assert.Empty(t, uow.heldSpec.Lines)
	assert.Equal(t, 1, idem.aborted, "refusal releases the idempotency record so the buyer can retry")
}

func TestHold_MixedNotAllowed(t *testing.T) {
	adm := admission3()
	adm.CfgAllowMixedCategories = false
	uc := newHoldUC(&fakeAdmissionRepo{admission: adm}, &fakeEventRepo{detail: detailWith(false, 3)}, &fakeIdem{}, &fakeUoW{})
	_, err := uc.Hold(context.Background(), holdCmd([]dto.HoldItem{
		{CategoryID: catA, Attendee: dto.AttendeeInput{NIK: nik1, FullName: "Alice"}},
		{CategoryID: catB, Attendee: dto.AttendeeInput{NIK: nik2, FullName: "Bob"}},
	}))
	assert.ErrorIs(t, err, inbound.ErrMixedNotAllowed)
}

func TestHold_DuplicateNIKInOrder(t *testing.T) {
	uc := newHoldUC(&fakeAdmissionRepo{admission: admission3()}, &fakeEventRepo{detail: detailWith(true, 3)}, &fakeIdem{}, &fakeUoW{})
	_, err := uc.Hold(context.Background(), holdCmd([]dto.HoldItem{
		{CategoryID: catA, Attendee: dto.AttendeeInput{NIK: nik1, FullName: "Alice"}},
		{CategoryID: catB, Attendee: dto.AttendeeInput{NIK: nik1, FullName: "Bob"}},
	}))
	assert.ErrorIs(t, err, inbound.ErrNikDuplicate)
}

func TestHold_InvalidHolderNIK(t *testing.T) {
	uc := newHoldUC(&fakeAdmissionRepo{admission: admission3()}, &fakeEventRepo{detail: detailWith(true, 3)}, &fakeIdem{}, &fakeUoW{})
	_, err := uc.Hold(context.Background(), holdCmd([]dto.HoldItem{
		{CategoryID: catA, Attendee: dto.AttendeeInput{NIK: "bad", FullName: "Alice"}},
	}))
	assert.ErrorIs(t, err, inbound.ErrNIKInvalid)
}

func TestHold_SessionExpired(t *testing.T) {
	adm := admission3()
	adm.ExpiresAt = time.Now().Add(-time.Minute)
	idem := &fakeIdem{}
	uc := newHoldUC(&fakeAdmissionRepo{admission: adm}, &fakeEventRepo{detail: detailWith(true, 3)}, idem, &fakeUoW{})
	_, err := uc.Hold(context.Background(), holdCmd(oneItem()))
	assert.ErrorIs(t, err, inbound.ErrSessionExpired)
	assert.Equal(t, 1, idem.aborted)
}

func TestHold_BuyerNikAtCapKeepsAdmissionAndAbortsRecord(t *testing.T) {
	idem := &fakeIdem{}
	uow := &fakeUoW{holdRes: outbound.HoldResult{Refused: outbound.RefusedBuyerNikLimit}}
	uc := newHoldUC(&fakeAdmissionRepo{admission: admission3()}, &fakeEventRepo{detail: detailWith(true, 3)}, idem, uow)
	_, err := uc.Hold(context.Background(), holdCmd(oneItem()))
	assert.ErrorIs(t, err, inbound.ErrBuyerNikLimitReached)
	assert.Equal(t, 422, inbound.ErrBuyerNikLimitReached.HTTPStatus)
	assert.Equal(t, 1, idem.aborted, "same key may be retried with another buyer NIK")
	assert.False(t, idem.completed)
}

func TestHold_IsRetryableRefusalTable(t *testing.T) {
	tests := []struct {
		name  string
		err   error
		abort int
	}{
		{"4xx typed", inbound.ErrCategoryUnavailable, 1},
		{"throttled 429", inbound.BuyerNikThrottled(5), 1},
		{"5xx dependency", inbound.ErrDependency, 0},
		{"plain error", assert.AnError, 0},
		{"context canceled", context.Canceled, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			idem := &fakeIdem{}
			uc := newHoldUC(&fakeAdmissionRepo{admission: admission3()}, &fakeEventRepo{detail: detailWith(true, 3)}, idem, &fakeUoW{holdErr: tt.err})
			_, err := uc.Hold(context.Background(), holdCmd(oneItem()))
			require.Error(t, err)
			assert.Equal(t, tt.abort, idem.aborted)
		})
	}
}

func TestHold_IdempotencyKeyReused(t *testing.T) {
	idem := &fakeIdem{found: true, rec: outbound.IdempotencyRecord{State: "COMPLETED", RequestHash: []byte("different")}}
	uc := newHoldUC(&fakeAdmissionRepo{admission: admission3()}, &fakeEventRepo{detail: detailWith(true, 3)}, idem, &fakeUoW{})
	_, err := uc.Hold(context.Background(), holdCmd(oneItem()))
	assert.ErrorIs(t, err, inbound.ErrIdemReused)
	assert.Zero(t, idem.aborted, "a reused key is not a refusal that frees the record")
}

// captureHash runs the usecase once to learn the request hash it computes for cmd.
func captureHash(t *testing.T, cmd inbound.HoldCommand) []byte {
	t.Helper()
	idem := &fakeIdem{}
	uc := newHoldUC(&fakeAdmissionRepo{admission: admission3()}, &fakeEventRepo{detail: detailWith(true, 3)}, idem,
		&fakeUoW{holdRes: outbound.HoldResult{BookingID: "bk-1", PublicRef: "RB-1", Version: 1, Status: "PENDING_PAYMENT", AmountMinor: 1000}})
	_, err := uc.Hold(context.Background(), cmd)
	require.NoError(t, err)
	require.NotEmpty(t, idem.gotHash)
	return idem.gotHash
}

func TestHold_RequestHashHoldsNoPlaintext(t *testing.T) {
	h := string(captureHash(t, holdCmd(oneItem())))
	for _, secret := range []string{buyerNIK, nik1, email} {
		assert.NotContains(t, h, secret, "idempotency request hash must not carry plaintext")
	}
}

func TestHold_ReplayReturnsStoredBodyWithoutReadingState(t *testing.T) {
	cmd := holdCmd(oneItem())
	stored, err := json.Marshal(dto.Booking{BookingID: "bk-replay", PublicRef: "RB-9", Status: "PENDING_PAYMENT", AmountMinor: 1000})
	require.NoError(t, err)

	idem := &fakeIdem{found: true, rec: outbound.IdempotencyRecord{State: "COMPLETED", RequestHash: captureHash(t, cmd), ResponseBody: stored}}
	adm := admission3()
	adm.Status = vo.AdmissionConverted // state moved on after the first success
	adm.OutstandingUnits = 0
	adms := &fakeAdmissionRepo{admission: adm}
	uow := &fakeUoW{}
	uc := newHoldUC(adms, &fakeEventRepo{detail: detailWith(true, 3)}, idem, uow)

	resp, err := uc.Hold(context.Background(), cmd)
	require.NoError(t, err)
	assert.Equal(t, "bk-replay", resp.BookingID, "returns the stored response, not a fresh hold")
	assert.Zero(t, adms.getCalls, "replay precedes every admission-state check")
	assert.Empty(t, uow.heldSpec.Lines, "replay must not call UoW.Hold again")
	assert.False(t, idem.completed)
}

func TestHold_ReplayInFlightIsTransient(t *testing.T) {
	cmd := holdCmd(oneItem())
	idem := &fakeIdem{found: true, rec: outbound.IdempotencyRecord{State: "IN_FLIGHT", RequestHash: captureHash(t, cmd)}}
	uc := newHoldUC(&fakeAdmissionRepo{admission: admission3()}, &fakeEventRepo{detail: detailWith(true, 3)}, idem, &fakeUoW{})
	_, err := uc.Hold(context.Background(), cmd)
	assert.ErrorIs(t, err, inbound.ErrDependency)
}

func TestHold_BadIdempotencyKey(t *testing.T) {
	uc := newHoldUC(&fakeAdmissionRepo{admission: admission3()}, &fakeEventRepo{detail: detailWith(true, 3)}, &fakeIdem{}, &fakeUoW{})
	cmd := holdCmd(oneItem())
	cmd.IdempotencyKey = "short"
	_, err := uc.Hold(context.Background(), cmd)
	assert.Error(t, err)
}

func TestHold_CancelledEventRefusesNewHolds(t *testing.T) {
	d := detailWith(true, 3)
	d.Event.Status = entity.EventCancelled
	uow := &fakeUoW{}
	uc := newHoldUC(&fakeAdmissionRepo{admission: admission3()}, &fakeEventRepo{detail: d}, &fakeIdem{}, uow)
	_, err := uc.Hold(context.Background(), holdCmd(oneItem()))
	assert.ErrorIs(t, err, inbound.ErrEventCancelled)
	assert.Empty(t, uow.heldSpec.Lines, "tidak ada stok yang ditahan")
}

func TestHold_AttendeeUseLoginNIK_Success(t *testing.T) {
	hashHex := "1e84a8b9c0d1e2f3a4b5c6d7e8f90a1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6e7"
	nikHash, _ := hex.DecodeString(hashHex)
	nikEnc := []byte("e:sealed")
	nikClient := &fakeUserNIKClient{nikEnc: nikEnc}
	uow := &fakeUoW{holdRes: outbound.HoldResult{BookingID: "bk-1", PublicRef: "RB-1", Version: 1, Status: "PENDING_PAYMENT", AmountMinor: 1000}}
	uc := newHoldUC(&fakeAdmissionRepo{admission: admission3()}, &fakeEventRepo{detail: detailWith(false, 3)}, &fakeIdem{}, uow).
		WithUserNIKClient(nikClient)

	cmd := inbound.HoldCommand{
		Request: dto.HoldRequest{
			SessionID: sessionID,
			Buyer:     dto.BuyerInput{UseLoginNIK: true, Email: email, EmailConfirmation: email, Consent: true},
			Items: []dto.HoldItem{
				{CategoryID: catA, Attendee: dto.AttendeeInput{UseLoginNIK: true, FullName: "Alice"}},
			},
		},
		VisitorID:      visitor,
		AccountID:      "acct-1",
		BuyerNIKHash:   nikHash,
		BuyerNIKMasked: "3174********0001",
		BearerToken:    "tok",
		IdempotencyKey: idemKey,
	}
	res, err := uc.Hold(context.Background(), cmd)
	require.NoError(t, err)
	assert.NotEmpty(t, res.BookingID)
}

func TestHold_AttendeeUseLoginNIK_NoNIKOnToken(t *testing.T) {
	nikClient := &fakeUserNIKClient{nikEnc: []byte("e:enc")}
	uc := newHoldUC(&fakeAdmissionRepo{admission: admission3()}, &fakeEventRepo{detail: detailWith(false, 3)}, &fakeIdem{}, &fakeUoW{}).
		WithUserNIKClient(nikClient)

	cmd := inbound.HoldCommand{
		Request: dto.HoldRequest{
			SessionID: sessionID,
			Buyer:     dto.BuyerInput{UseLoginNIK: true, Email: email, EmailConfirmation: email, Consent: true},
			Items: []dto.HoldItem{
				{CategoryID: catA, Attendee: dto.AttendeeInput{UseLoginNIK: true, FullName: "Alice"}},
			},
		},
		VisitorID:      visitor,
		AccountID:      "acct-1",
		BuyerNIKHash:   nil, // no NIK in token
		BearerToken:    "tok",
		IdempotencyKey: idemKey,
	}
	_, err := uc.Hold(context.Background(), cmd)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "UNAUTHORIZED")
}

func TestHold_UnknownCategoryListsIDInMessage(t *testing.T) {
	uc := newHoldUC(&fakeAdmissionRepo{admission: admission3()}, &fakeEventRepo{detail: detailWith(true, 3)}, &fakeIdem{}, &fakeUoW{})
	unknown := "99999999-9999-9999-9999-999999999999"
	_, err := uc.Hold(context.Background(), holdCmd([]dto.HoldItem{{CategoryID: unknown, Attendee: dto.AttendeeInput{NIK: nik1, FullName: "Alice"}}}))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "CATEGORY_UNAVAILABLE")
	assert.Contains(t, err.Error(), unknown)
}
