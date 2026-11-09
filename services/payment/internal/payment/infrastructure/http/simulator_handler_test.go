package http_test

import (
	"encoding/json"
	stdhttp "net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/payment/internal/payment/domain/dto"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/payment/internal/payment/port/inbound"
)

const (
	simAttemptID = "33333333-3333-3333-3333-333333333333"
	simPath      = "/api/v1/local-simulator/payments/" + simAttemptID + "/scenario"
)

func simReq(e env, body string, withToken bool) *stdhttp.Request {
	r := jsonReq(stdhttp.MethodPost, simPath, body)
	if withToken {
		r.Header.Set("X-Visitor-Token", e.visitorToken())
	}
	return r
}

// P-15: authentication, decoding, error mapping and the 202 body of the local simulator.
func TestSimulatorHandler_Success(t *testing.T) {
	e := newEnv(t)
	e.simulate.EXPECT().Simulate(gomock.Any(), inbound.Payer{VisitorID: visitorID}, simAttemptID, "SETTLE").
		Return(dto.SimulatorResult{Simulated: true, Scenario: "SETTLE"}, nil)

	rec := e.do(simReq(e, `{"scenario":"SETTLE"}`, true))
	require.Equal(t, stdhttp.StatusAccepted, rec.Code, rec.Body.String())
	var body struct {
		Meta struct{ Code int }
		Data dto.SimulatorResult
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, 202, body.Meta.Code)
	assert.Equal(t, dto.SimulatorResult{Simulated: true, Scenario: "SETTLE"}, body.Data)
}

func TestSimulatorHandler_Rejections(t *testing.T) {
	t.Run("no token is 401", func(t *testing.T) {
		e := newEnv(t)
		rec := e.do(simReq(e, `{"scenario":"SETTLE"}`, false))
		assert.Equal(t, stdhttp.StatusUnauthorized, rec.Code)
	})
	t.Run("invalid token is 401", func(t *testing.T) {
		e := newEnv(t)
		r := simReq(e, `{"scenario":"SETTLE"}`, false)
		r.Header.Set("X-Visitor-Token", "bukan.token")
		assert.Equal(t, stdhttp.StatusUnauthorized, e.do(r).Code)
	})
	t.Run("rb_pay cookie alone is not accepted", func(t *testing.T) {
		e := newEnv(t)
		r := simReq(e, `{"scenario":"SETTLE"}`, false)
		r.AddCookie(&stdhttp.Cookie{Name: "rb_pay", Value: e.pageCookie(visitorID, bookingID)})
		assert.Equal(t, stdhttp.StatusUnauthorized, e.do(r).Code)
	})
	t.Run("unknown field is 400", func(t *testing.T) {
		e := newEnv(t)
		assert.Equal(t, stdhttp.StatusBadRequest, e.do(simReq(e, `{"scenario":"SETTLE","x":1}`, true)).Code)
	})
	t.Run("malformed body is 400", func(t *testing.T) {
		e := newEnv(t)
		assert.Equal(t, stdhttp.StatusBadRequest, e.do(simReq(e, `{bukan`, true)).Code)
	})
}

func TestSimulatorHandler_UsecaseErrorsUseTheEnvelope(t *testing.T) {
	tests := []struct {
		name   string
		err    error
		status int
		prefix string
	}{
		{"bad scenario", inbound.Validation("scenario", "scenario tidak dikenal"), stdhttp.StatusBadRequest, "VALIDATION_ERROR"},
		{"foreign or unknown attempt", inbound.ErrNotFound, stdhttp.StatusNotFound, "NOT_FOUND"},
		{"dependency", inbound.ErrDependency, stdhttp.StatusServiceUnavailable, "DEPENDENCY_UNAVAILABLE"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			e.simulate.EXPECT().Simulate(gomock.Any(), gomock.Any(), simAttemptID, "REFUND").Return(dto.SimulatorResult{}, tc.err)
			rec := e.do(simReq(e, `{"scenario":"REFUND"}`, true))
			assert.Equal(t, tc.status, rec.Code)
			assert.Contains(t, errorMessage(t, rec), tc.prefix)
		})
	}
}
