package midtrans_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	vo "github.com/tiket-sampai-nonton/rusuh-booking-be/services/payment/internal/payment/domain/valueobject"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/payment/internal/payment/port/outbound"
)

func TestChargeSnap_BuildsRequestAndReturnsToken(t *testing.T) {
	var got map[string]any
	var hdr http.Header
	var path string
	g, _ := newGateway(t, capture(&got, &hdr, &path, `{"token":"snap-tok-1","redirect_url":"https://app.sandbox.midtrans.com/snap/v4/redirection/snap-tok-1"}`))

	res, err := g.Charge(context.Background(), withFinish(chargeReq(vo.MethodSnap)))
	require.NoError(t, err)

	assert.Equal(t, "POST /snap/v1/transactions", path)
	assert.Contains(t, hdr.Get("Authorization"), "Basic ")
	td := got["transaction_details"].(map[string]any)
	assert.Equal(t, "RB-7F3A9C21-1", td["order_id"])
	assert.EqualValues(t, 1500000, td["gross_amount"])
	exp := got["expiry"].(map[string]any)
	assert.Equal(t, "minute", exp["unit"])
	assert.EqualValues(t, 9, exp["duration"]) // 9m30s rounds down: never outlives the booking
	assert.Equal(t, "2026-10-26 17:00:00 +0700", exp["start_time"])
	assert.Equal(t, map[string]any{"finish": "https://fe/ok"}, got["callbacks"])
	assert.Equal(t, "SNAP", res.Instruction.Type)
	assert.Equal(t, "snap-tok-1", res.Instruction.SnapToken)
	assert.Contains(t, res.Instruction.RedirectURL, "snap-tok-1")
}

func TestChargeSnap_Failures(t *testing.T) {
	short := chargeReq(vo.MethodSnap)
	short.Expiry = 50 * time.Second
	g, _ := newGateway(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { reply(w, 200, `{}`) }))
	_, err := g.Charge(context.Background(), short)
	assert.ErrorIs(t, err, outbound.ErrGatewayRejected)

	_, err = g.Charge(context.Background(), chargeReq(vo.MethodSnap))
	assert.ErrorIs(t, err, outbound.ErrGatewayAmbiguous, "respons tanpa token bukan sukses")

	g, _ = newGateway(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { reply(w, 401, `{"error_messages":["unauthorized"]}`) }))
	_, err = g.Charge(context.Background(), chargeReq(vo.MethodSnap))
	assert.ErrorIs(t, err, outbound.ErrGatewayRejected)
}

func withFinish(r outbound.ChargeRequest) outbound.ChargeRequest {
	r.FinishURL = "https://fe/ok"
	return r
}
