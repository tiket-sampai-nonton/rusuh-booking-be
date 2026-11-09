package usecase_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/apperror"

	mock_outbound "github.com/tiket-sampai-nonton/rusuh-booking-be/services/payment/_mock/payment/port/outbound"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/payment/internal/payment/application/usecase"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/payment/internal/payment/domain/dto"
	vo "github.com/tiket-sampai-nonton/rusuh-booking-be/services/payment/internal/payment/domain/valueobject"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/payment/internal/payment/port/inbound"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/payment/internal/payment/port/outbound"
)

const simServerKey = "SB-Mid-server-secret-key-do-not-log"

// recWebhook records what the simulator submits to the real webhook path.
type recWebhook struct {
	got  []dto.MidtransNotification
	raws [][]byte
	err  error
}

func (r *recWebhook) HandleWebhook(_ context.Context, n dto.MidtransNotification, raw []byte) error {
	r.got = append(r.got, n)
	r.raws = append(r.raws, raw)
	return r.err
}

type simEnv struct {
	m      mocks
	hook   *recWebhook
	faults *mock_outbound.MockIGatewayFaults
	uc     *usecase.SimulatePayment
}

func newSimEnv(t *testing.T) simEnv {
	t.Helper()
	m := newMocks(t)
	faults := mock_outbound.NewMockIGatewayFaults(gomock.NewController(t))
	hook := &recWebhook{}
	uc := usecase.NewSimulatePayment(m.attempts, m.booking, hook, testSigner{key: simServerKey}, faults, clk)
	return simEnv{m: m, hook: hook, faults: faults, uc: uc}
}

// ownedAttempt expects the lookup + ownership calls of a valid request.
func (e simEnv) ownedAttempt(st vo.AttemptStatus) {
	e.m.attempts.EXPECT().FindByID(gomock.Any(), attemptID).Return(attempt(st), true, nil)
	e.m.booking.EXPECT().GetPayable(gomock.Any(), bookingID).Return(payable(), nil)
}

// testSigner stands in for the Midtrans signer adapter (usecases depend on ports only); the real formula is
// covered in infrastructure/midtrans and, end to end, in delivery.
type testSigner struct{ key string }

func (t testSigner) Sign(order, code, gross string) string {
	return "SIG|" + order + "|" + code + "|" + gross + "|" + t.key
}

func sig(order, code, gross string) string {
	return testSigner{key: simServerKey}.Sign(order, code, gross)
}

func TestSimulate_RejectsBeforeAnyLookup(t *testing.T) {
	for _, sc := range []string{"", "settle", "REFUND", "SETTLE "} {
		t.Run("scenario="+sc, func(t *testing.T) {
			e := newSimEnv(t) // strict mocks: any repo/booking call fails the test
			_, err := e.uc.Simulate(ctx(), payer, attemptID, sc)
			var ae *apperror.Error
			require.ErrorAs(t, err, &ae)
			assert.Equal(t, 400, ae.HTTPStatus)
			assert.Empty(t, e.hook.got)
		})
	}
}

func TestSimulate_NotFoundCases(t *testing.T) {
	t.Run("non uuid attempt", func(t *testing.T) {
		e := newSimEnv(t)
		_, err := e.uc.Simulate(ctx(), payer, "bukan-uuid", "SETTLE")
		assert.ErrorIs(t, err, inbound.ErrNotFound)
	})
	t.Run("unknown attempt", func(t *testing.T) {
		e := newSimEnv(t)
		e.m.attempts.EXPECT().FindByID(gomock.Any(), attemptID).Return(attempt(vo.AttemptPending), false, nil)
		_, err := e.uc.Simulate(ctx(), payer, attemptID, "SETTLE")
		assert.ErrorIs(t, err, inbound.ErrNotFound)
		assert.Empty(t, e.hook.got)
	})
	t.Run("foreign buyer", func(t *testing.T) {
		e := newSimEnv(t)
		e.m.attempts.EXPECT().FindByID(gomock.Any(), attemptID).Return(attempt(vo.AttemptPending), true, nil)
		e.m.booking.EXPECT().GetPayable(gomock.Any(), bookingID).Return(payable(), nil)
		_, err := e.uc.Simulate(ctx(), inbound.Payer{VisitorID: "99999999-9999-9999-9999-999999999999"}, attemptID, "SETTLE")
		assert.ErrorIs(t, err, inbound.ErrNotFound)
		assert.Empty(t, e.hook.got)
	})
	t.Run("booking down is 503", func(t *testing.T) {
		e := newSimEnv(t)
		e.m.attempts.EXPECT().FindByID(gomock.Any(), attemptID).Return(attempt(vo.AttemptPending), true, nil)
		e.m.booking.EXPECT().GetPayable(gomock.Any(), bookingID).Return(outbound.Payable{}, errors.New("down"))
		_, err := e.uc.Simulate(ctx(), payer, attemptID, "SETTLE")
		assert.ErrorIs(t, err, inbound.ErrDependency)
	})
	t.Run("repository error is 503", func(t *testing.T) {
		e := newSimEnv(t)
		e.m.attempts.EXPECT().FindByID(gomock.Any(), attemptID).Return(attempt(vo.AttemptPending), false, errors.New("db"))
		_, err := e.uc.Simulate(ctx(), payer, attemptID, "SETTLE")
		assert.ErrorIs(t, err, inbound.ErrDependency)
	})
}

type wantN struct{ code, status, settle string }

func TestSimulate_ScenarioNotifications(t *testing.T) {
	a := attempt(vo.AttemptPending)
	nowWIB := now.In(vo.WIB()).Format("2006-01-02 15:04:05")
	lateWIB := a.ExpiresAt.Add(-time.Second).In(vo.WIB()).Format("2006-01-02 15:04:05")
	tests := []struct {
		scenario string
		want     []wantN
	}{
		{"SETTLE", []wantN{{"200", "settlement", nowWIB}}},
		{"SETTLE_LATE", []wantN{{"200", "settlement", lateWIB}}},
		{"EXPIRE", []wantN{{"407", "expire", ""}}},
		{"DENY", []wantN{{"202", "deny", ""}}},
		{"DUPLICATE_WEBHOOK", []wantN{{"200", "settlement", nowWIB}, {"200", "settlement", nowWIB}}},
		{"OUT_OF_ORDER", []wantN{{"200", "settlement", nowWIB}, {"201", "pending", ""}}},
	}
	for _, tc := range tests {
		t.Run(tc.scenario, func(t *testing.T) {
			e := newSimEnv(t)
			e.ownedAttempt(vo.AttemptPending)
			e.faults.EXPECT().Clear(a.ProviderOrderID)

			res, err := e.uc.Simulate(ctx(), payer, attemptID, tc.scenario)
			require.NoError(t, err)
			assert.Equal(t, dto.SimulatorResult{Simulated: true, Scenario: tc.scenario}, res)

			require.Len(t, e.hook.got, len(tc.want))
			for i, w := range tc.want {
				n := e.hook.got[i]
				assert.Equal(t, a.ProviderOrderID, n.OrderID)
				assert.Equal(t, "1500000.00", n.GrossAmount)
				assert.Equal(t, w.code, n.StatusCode)
				assert.Equal(t, w.status, n.TransactionStatus)
				assert.Equal(t, w.settle, n.SettlementTime)
				assert.Equal(t, "SIM-"+a.ID, n.TransactionID)
				assert.Equal(t, sig(n.OrderID, n.StatusCode, n.GrossAmount), n.SignatureKey, "signed like Midtrans")
				var back dto.MidtransNotification
				require.NoError(t, json.Unmarshal(e.hook.raws[i], &back))
				assert.Equal(t, n, back, "raw body is the notification JSON")
			}
			if tc.scenario == "DUPLICATE_WEBHOOK" {
				assert.Equal(t, e.hook.raws[0], e.hook.raws[1], "byte-identical duplicate")
			}
		})
	}
}

func TestSimulate_TimeoutArmsFaultAndSendsNoWebhook(t *testing.T) {
	e := newSimEnv(t)
	e.ownedAttempt(vo.AttemptPending)
	e.faults.EXPECT().ArmTimeout("RB-7F3A9C21-1", 2*time.Minute).Times(1)
	res, err := e.uc.Simulate(ctx(), payer, attemptID, "TIMEOUT")
	require.NoError(t, err)
	assert.Equal(t, "TIMEOUT", res.Scenario)
	assert.Empty(t, e.hook.got)
}

// P-8 (invariant 5): the simulator only ever FindByID on the repo and never writes a booking; gomock's strict
// controller fails the test if Transition (or anything else) is called.
func TestSimulate_NeverTouchesAttemptStateDirectly(t *testing.T) {
	e := newSimEnv(t)
	e.ownedAttempt(vo.AttemptCreated)
	e.faults.EXPECT().Clear(gomock.Any())
	_, err := e.uc.Simulate(ctx(), payer, attemptID, "SETTLE")
	require.NoError(t, err)
	assert.Len(t, e.hook.got, 1)
}

func TestSimulate_WebhookErrors(t *testing.T) {
	t.Run("dependency error stays 503", func(t *testing.T) {
		e := newSimEnv(t)
		e.ownedAttempt(vo.AttemptPending)
		e.faults.EXPECT().Clear(gomock.Any())
		e.hook.err = inbound.ErrDependency
		_, err := e.uc.Simulate(ctx(), payer, attemptID, "SETTLE")
		assert.ErrorIs(t, err, inbound.ErrDependency)
	})
	t.Run("first error aborts the sequence", func(t *testing.T) {
		e := newSimEnv(t)
		e.ownedAttempt(vo.AttemptPending)
		e.faults.EXPECT().Clear(gomock.Any())
		e.hook.err = inbound.ErrDependency
		_, err := e.uc.Simulate(ctx(), payer, attemptID, "OUT_OF_ORDER")
		require.Error(t, err)
		assert.Len(t, e.hook.got, 1)
	})
}

// P-16 (Req 2.13): no secret or identifier leaks in the response or the log; the log says it is simulated.
func TestSimulate_NoLeakInResponseOrLogs(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	e := newSimEnv(t)
	e.ownedAttempt(vo.AttemptPending)
	e.faults.EXPECT().Clear(gomock.Any())
	res, err := e.uc.Simulate(ctx(), payer, attemptID, "SETTLE")
	require.NoError(t, err)

	body, _ := json.Marshal(res)
	logs := buf.String()
	assert.Contains(t, logs, `"simulated":true`)
	assert.Contains(t, logs, `"scenario":"SETTLE"`)
	for _, secret := range []string{simServerKey, sig("RB-7F3A9C21-1", "200", "1500000.00"), visitorID, "signature_key"} {
		assert.NotContains(t, logs, secret)
		assert.NotContains(t, string(body), secret)
	}
}
