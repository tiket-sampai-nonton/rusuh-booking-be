package usecase_test

import (
	"context"
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

type joinMocks struct {
	store    *mock_outbound.MockIQueueStore
	settings *mock_outbound.MockIEventSettingsRepo
	booking  *mock_outbound.MockIBookingClient
	limiter  *mock_outbound.MockIRateLimiter
	tokens   *mock_outbound.MockITokenIssuer
	verifier *mock_outbound.MockIUserTokenVerifier
	pii      *mock_outbound.MockIPIIProtector
	audit    *mock_outbound.MockIEntryAuditRepo
}

func newJoin(t *testing.T) (*usecase.JoinQueue, joinMocks) {
	t.Helper()
	ctrl := gomock.NewController(t)
	m := joinMocks{
		store:    mock_outbound.NewMockIQueueStore(ctrl),
		settings: mock_outbound.NewMockIEventSettingsRepo(ctrl),
		booking:  mock_outbound.NewMockIBookingClient(ctrl),
		limiter:  mock_outbound.NewMockIRateLimiter(ctrl),
		tokens:   mock_outbound.NewMockITokenIssuer(ctrl),
		verifier: mock_outbound.NewMockIUserTokenVerifier(ctrl),
		pii:      mock_outbound.NewMockIPIIProtector(ctrl),
		audit:    mock_outbound.NewMockIEntryAuditRepo(ctrl),
	}
	uc := usecase.NewJoinQueue(usecase.JoinDeps{
		Store: m.store, Settings: m.settings, Booking: m.booking, Limiter: m.limiter,
		Tokens: m.tokens, UserVerifier: m.verifier, PII: m.pii, Audit: m.audit,
		Clock: clock.Fixed{T: time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC)},
	}, usecase.JoinSettings{
		JoinPerEmail: 5, JoinPerEmailWindow: time.Minute, JoinPerIP: 60, JoinPerIPWindow: time.Minute,
		ClaimWindow: time.Minute, StatusURLTemplate: "/api/v1/queue-status/",
	})
	return uc, m
}

func validBody() dto.JoinRequest {
	// 3201 = Jawa Barat/Bogor (province 32, kab 01, kec 01)
	// 010190 = DD=01, MM=01, YY=90 (→ 1990-Jan-01); serial = 0001
	return dto.JoinRequest{BuyerNik: "3201010101900001", Email: "a@b.id", EmailConfirmation: "a@b.id", ConsentNik: true}
}

func TestJoin_GuestWaiting(t *testing.T) {
	uc, m := newJoin(t)
	ctx := context.Background()

	m.pii.EXPECT().HashEmail(gomock.Any()).Return([]byte{0x01}).AnyTimes()
	m.pii.EXPECT().HashNIK("3201010101900001").Return([]byte{0xaa})
	m.pii.EXPECT().SealEmail(gomock.Any()).Return([]byte("enc"), nil)
	m.limiter.EXPECT().Allow(ctx, gomock.Any(), 5, time.Minute).Return(true, nil)
	m.limiter.EXPECT().Allow(ctx, gomock.Any(), 60, time.Minute).Return(true, nil)
	m.settings.EXPECT().Get(ctx, "ev1").Return(entity.EventSettings{ActiveSlotCapacity: 0, OneActivePerVisitor: true, OneActivePerEmail: true}, true, nil)
	m.store.EXPECT().Join(ctx, gomock.Any()).Return(outbound.JoinResult{Outcome: outbound.JoinOK, Seq: 7}, nil)
	m.audit.EXPECT().Append(ctx, gomock.Any()).Return(nil)
	m.tokens.EXPECT().IssueQueue(gomock.Any(), gomock.Any(), gomock.Any()).Return("qt", nil)
	m.tokens.EXPECT().IssueVisitor(gomock.Any(), gomock.Any()).Return("vt", nil)

	resp, err := uc.Join(ctx, inbound.JoinInput{EventID: "ev1", Body: validBody(), ClientIP: "1.2.3.4"})
	require.NoError(t, err)
	assert.Equal(t, "WAITING", resp.Status)
	assert.Equal(t, int64(7), resp.Seq)
	assert.Equal(t, "qt", resp.QueueToken)
	assert.Equal(t, "vt", resp.VisitorToken)
	assert.Equal(t, "/api/v1/queue-status/ev1", resp.StatusURL)
}

func TestJoin_DirectAdmission(t *testing.T) {
	uc, m := newJoin(t)
	ctx := context.Background()

	m.pii.EXPECT().HashEmail(gomock.Any()).Return([]byte{0x01}).AnyTimes()
	m.pii.EXPECT().HashNIK(gomock.Any()).Return([]byte{0xaa})
	m.pii.EXPECT().SealEmail(gomock.Any()).Return([]byte("enc"), nil)
	m.pii.EXPECT().SealBuyerEmail("a@b.id").Return("c2VhbGVk", nil)
	m.limiter.EXPECT().Allow(ctx, gomock.Any(), gomock.Any(), gomock.Any()).Return(true, nil).Times(2)
	m.settings.EXPECT().Get(ctx, "ev1").Return(entity.EventSettings{ActiveSlotCapacity: 10}, true, nil)
	m.store.EXPECT().Join(ctx, gomock.Any()).DoAndReturn(func(_ context.Context, p outbound.JoinParams) (outbound.JoinResult, error) {
		assert.Equal(t, "3201********0001", p.NikMasked, "NIK tersamar disimpan di entry")
		return outbound.JoinResult{Outcome: outbound.JoinOK, Seq: 1}, nil
	})
	m.audit.EXPECT().Append(ctx, gomock.Any()).Return(nil).Times(2) // JOINED + ADMITTED
	m.store.EXPECT().ActiveCount(ctx, "ev1").Return(0, nil)
	m.booking.EXPECT().ReserveBatch(ctx, gomock.Any()).DoAndReturn(
		func(_ context.Context, req outbound.AllocationBatchRequest) (outbound.AllocationBatchResponse, error) {
			// Booking stores the buyer's email (e-ticket) and masked NIK; the plain NIK never leaves the queue.
			require.Len(t, req.Entries, 1)
			assert.Equal(t, "c2VhbGVk", req.Entries[0].BuyerEmailEnc, "Booking hanya menerima ciphertext")
			assert.Equal(t, "3201********0001", req.Entries[0].BuyerNikMasked)
			return outbound.AllocationBatchResponse{
				Allocations: []outbound.AllocationResult{{QueueEntryID: "x", AllocationID: "al1", Status: "GRANTED", ClaimDeadline: "2026-10-20T10:01:00Z"}},
			}, nil
		})
	m.store.EXPECT().Promote(ctx, "ev1", gomock.Any(), 10, gomock.Any()).Return([]string{"x"}, nil)
	m.tokens.EXPECT().IssueQueue(gomock.Any(), gomock.Any(), gomock.Any()).Return("qt", nil)
	m.tokens.EXPECT().IssueVisitor(gomock.Any(), gomock.Any()).Return("vt", nil)

	resp, err := uc.Join(ctx, inbound.JoinInput{EventID: "ev1", Body: validBody(), ClientIP: "1.2.3.4"})
	require.NoError(t, err)
	assert.Equal(t, "ADMITTED", resp.Status)
	assert.NotEmpty(t, resp.ClaimDeadline)
}

func TestJoin_LoggedInWithNIKIgnoresBodyNik(t *testing.T) {
	uc, m := newJoin(t)
	ctx := context.Background()

	m.verifier.EXPECT().Verify("bearer").Return(outbound.UserIdentity{AccountID: "acc1", HasNIK: true, NIKHash: "deadbeef"}, nil)
	m.pii.EXPECT().HashEmail(gomock.Any()).Return([]byte{0x01}).AnyTimes()
	m.pii.EXPECT().SealEmail(gomock.Any()).Return([]byte("enc"), nil)
	m.limiter.EXPECT().Allow(ctx, gomock.Any(), gomock.Any(), gomock.Any()).Return(true, nil).Times(1)
	m.settings.EXPECT().Get(ctx, "ev1").Return(entity.EventSettings{ActiveSlotCapacity: 0}, true, nil)
	m.store.EXPECT().Join(ctx, gomock.AssignableToTypeOf(outbound.JoinParams{})).DoAndReturn(
		func(_ context.Context, p outbound.JoinParams) (outbound.JoinResult, error) {
			assert.Equal(t, "deadbeef", p.NikHash)
			assert.Equal(t, "acc1", p.AccountID)
			return outbound.JoinResult{Outcome: outbound.JoinOK, Seq: 3}, nil
		})
	m.audit.EXPECT().Append(ctx, gomock.Any()).Return(nil)
	m.tokens.EXPECT().IssueQueue(gomock.Any(), gomock.Any(), gomock.Any()).Return("qt", nil)

	body := validBody()
	body.BuyerNik = "" // no body NIK needed; token supplies it
	resp, err := uc.Join(ctx, inbound.JoinInput{EventID: "ev1", Body: body, Bearer: "bearer", VisitorID: "v-existing"})
	require.NoError(t, err)
	assert.Equal(t, "WAITING", resp.Status)
	assert.Empty(t, resp.VisitorToken, "existing visitor gets no new token")
}

func TestJoin_Validation(t *testing.T) {
	tests := []struct {
		name string
		body dto.JoinRequest
		want error
	}{
		{"no consent", dto.JoinRequest{Email: "a@b.id", EmailConfirmation: "a@b.id", BuyerNik: "3201234567890123"}, entity.ErrConsentNik},
		{"email mismatch", dto.JoinRequest{Email: "a@b.id", EmailConfirmation: "x@b.id", ConsentNik: true, BuyerNik: "3201234567890123"}, entity.ErrEmailMismatch},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			uc, _ := newJoin(t)
			_, err := uc.Join(context.Background(), inbound.JoinInput{EventID: "ev1", Body: tt.body})
			assert.ErrorIs(t, err, tt.want)
		})
	}
}

func TestJoin_GuestNikRequired(t *testing.T) {
	uc, _ := newJoin(t)
	body := validBody()
	body.BuyerNik = ""
	_, err := uc.Join(context.Background(), inbound.JoinInput{EventID: "ev1", Body: body})
	assert.ErrorIs(t, err, entity.ErrNikRequired)
}

func TestJoin_NikInvalidFormat(t *testing.T) {
	uc, _ := newJoin(t)
	body := validBody()
	body.BuyerNik = "123"
	_, err := uc.Join(context.Background(), inbound.JoinInput{EventID: "ev1", Body: body})
	assert.ErrorIs(t, err, entity.ErrNikInvalid)
}

func TestJoin_NikInvalidStructure(t *testing.T) {
	// These NIKs are 16 digits (pass the fast regex check) but fail pkg/nik structural validation.
	tests := []struct {
		name string
		nik  string
	}{
		// Province code 00 is invalid (not in Kemendagri list).
		{"invalid province", "0000010101900001"},
		// kab/kota code 00 is invalid (Kemendagri rule).
		{"invalid kab", "3100010101900001"},
		// Serial 0000 is invalid.
		{"serial zero", "3201010101010000"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			uc, _ := newJoin(t)
			body := validBody()
			body.BuyerNik = tt.nik
			_, err := uc.Join(context.Background(), inbound.JoinInput{EventID: "ev1", Body: body})
			assert.ErrorIs(t, err, entity.ErrNikInvalid, "NIK %q should be rejected", tt.nik)
		})
	}
}

func TestJoin_RateLimited(t *testing.T) {
	uc, m := newJoin(t)
	ctx := context.Background()
	m.pii.EXPECT().HashEmail(gomock.Any()).Return([]byte{0x01}).AnyTimes()
	m.pii.EXPECT().HashNIK(gomock.Any()).Return([]byte{0xaa})
	m.limiter.EXPECT().Allow(ctx, gomock.Any(), 5, time.Minute).Return(false, nil)
	_, err := uc.Join(ctx, inbound.JoinInput{EventID: "ev1", Body: validBody(), ClientIP: "1.2.3.4"})
	ae := inbound.MapError(err)
	assert.Equal(t, 429, ae.HTTPStatus)
}

func TestJoin_ExistingConflicts(t *testing.T) {
	tests := []struct {
		name    string
		outcome outbound.JoinOutcome
		want    error
	}{
		{"nik", outbound.JoinExistingNIK, entity.ErrNikInQueue},
		{"visitor", outbound.JoinExistingVisit, entity.ErrVisitorInQueue},
		{"email", outbound.JoinExistingEmail, entity.ErrEmailInQueue},
		{"quota", outbound.JoinQuotaExhausted, entity.ErrQuotaExhausted},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			uc, m := newJoin(t)
			ctx := context.Background()
			m.pii.EXPECT().HashEmail(gomock.Any()).Return([]byte{0x01}).AnyTimes()
			m.pii.EXPECT().HashNIK(gomock.Any()).Return([]byte{0xaa})
			m.pii.EXPECT().SealEmail(gomock.Any()).Return([]byte("enc"), nil)
			m.limiter.EXPECT().Allow(ctx, gomock.Any(), gomock.Any(), gomock.Any()).Return(true, nil).Times(2)
			m.settings.EXPECT().Get(ctx, "ev1").Return(entity.EventSettings{}, true, nil)
			m.store.EXPECT().Join(ctx, gomock.Any()).Return(outbound.JoinResult{Outcome: tt.outcome}, nil)
			_, err := uc.Join(ctx, inbound.JoinInput{EventID: "ev1", Body: validBody(), ClientIP: "1.2.3.4"})
			assert.ErrorIs(t, err, tt.want)
		})
	}
}

func TestJoin_StorePropagatesError(t *testing.T) {
	uc, m := newJoin(t)
	ctx := context.Background()
	m.pii.EXPECT().HashEmail(gomock.Any()).Return([]byte{0x01}).AnyTimes()
	m.pii.EXPECT().HashNIK(gomock.Any()).Return([]byte{0xaa})
	m.pii.EXPECT().SealEmail(gomock.Any()).Return([]byte("enc"), nil)
	m.limiter.EXPECT().Allow(ctx, gomock.Any(), gomock.Any(), gomock.Any()).Return(true, nil).Times(2)
	m.settings.EXPECT().Get(ctx, "ev1").Return(entity.EventSettings{}, true, nil)
	m.store.EXPECT().Join(ctx, gomock.Any()).Return(outbound.JoinResult{}, errors.New("valkey down"))
	_, err := uc.Join(ctx, inbound.JoinInput{EventID: "ev1", Body: validBody(), ClientIP: "1.2.3.4"})
	assert.Error(t, err)
}

func TestJoin_FirstJoinForKnownEventCreatesDefaultSettings(t *testing.T) {
	uc, m := newJoin(t)
	ctx := context.Background()

	m.pii.EXPECT().HashEmail(gomock.Any()).Return([]byte{0x01}).AnyTimes()
	m.pii.EXPECT().HashNIK(gomock.Any()).Return([]byte{0xaa})
	m.pii.EXPECT().SealEmail(gomock.Any()).Return([]byte("enc"), nil)
	m.limiter.EXPECT().Allow(ctx, gomock.Any(), gomock.Any(), gomock.Any()).Return(true, nil).Times(2)
	m.settings.EXPECT().Get(ctx, "ev1").Return(entity.EventSettings{}, false, nil)
	m.booking.EXPECT().Supply(ctx, "ev1").Return(outbound.SupplySnapshot{EventID: "ev1"}, nil)
	m.settings.EXPECT().Ensure(ctx, "ev1").Return(entity.EventSettings{ActiveSlotCapacity: 0}, nil)
	m.store.EXPECT().Join(ctx, gomock.Any()).Return(outbound.JoinResult{Outcome: outbound.JoinOK, Seq: 1}, nil)
	m.audit.EXPECT().Append(ctx, gomock.Any()).Return(nil)
	m.tokens.EXPECT().IssueQueue(gomock.Any(), gomock.Any(), gomock.Any()).Return("qt", nil)
	m.tokens.EXPECT().IssueVisitor(gomock.Any(), gomock.Any()).Return("vt", nil)

	resp, err := uc.Join(ctx, inbound.JoinInput{EventID: "ev1", Body: validBody(), ClientIP: "1.2.3.4"})
	require.NoError(t, err)
	assert.Equal(t, "WAITING", resp.Status)
}

func TestJoin_UnknownEventIsRefusedWithoutTouchingTheQueue(t *testing.T) {
	uc, m := newJoin(t)
	ctx := context.Background()

	m.pii.EXPECT().HashEmail(gomock.Any()).Return([]byte{0x01}).AnyTimes()
	m.pii.EXPECT().HashNIK(gomock.Any()).Return([]byte{0xaa})
	m.pii.EXPECT().SealEmail(gomock.Any()).Return([]byte("enc"), nil)
	m.limiter.EXPECT().Allow(ctx, gomock.Any(), gomock.Any(), gomock.Any()).Return(true, nil).Times(2)
	m.settings.EXPECT().Get(ctx, "nope").Return(entity.EventSettings{}, false, nil)
	m.booking.EXPECT().Supply(ctx, "nope").Return(outbound.SupplySnapshot{}, entity.ErrEventNotFound)
	// no settings.Ensure, no store.Join: gomock fails the test on any unexpected call

	_, err := uc.Join(ctx, inbound.JoinInput{EventID: "nope", Body: validBody(), ClientIP: "1.2.3.4"})
	require.ErrorIs(t, err, entity.ErrEventNotFound)
	assert.Equal(t, 404, inbound.MapError(err).HTTPStatus)
}
