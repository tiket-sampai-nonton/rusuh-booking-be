package delivery

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/payment/internal/config"
)

// P-14: the simulator exists only for local and ci.
func TestSimulatorEnabled(t *testing.T) {
	tests := map[string]bool{"local": true, "ci": true, "dev": false, "staging": false, "prod": false, "": false, "LOCAL": false}
	for env, want := range tests {
		t.Run("env="+env, func(t *testing.T) {
			assert.Equal(t, want, simulatorEnabled(env))
		})
	}
}

func TestCheckSimulatorEnv_RefusesProductionMidtrans(t *testing.T) {
	tests := []struct {
		appEnv, midtrans string
		wantErr          bool
	}{
		{"local", config.MidtransSandbox, false},
		{"ci", config.MidtransSandbox, false},
		{"local", config.MidtransProduction, true},
		{"ci", config.MidtransProduction, true},
		{"prod", config.MidtransProduction, false}, // simulator not enabled: nothing to refuse
		{"staging", config.MidtransSandbox, false},
	}
	for _, tc := range tests {
		t.Run(tc.appEnv+"/"+tc.midtrans, func(t *testing.T) {
			err := checkSimulatorEnv(tc.appEnv, tc.midtrans)
			if tc.wantErr {
				assert.ErrorContains(t, err, "simulator")
				return
			}
			assert.NoError(t, err)
		})
	}
}

// P-14: the composition root refuses to start with the simulator and production Midtrans together.
func TestNewPaymentRegistrars_RefusesSimulatorWithProductionMidtrans(t *testing.T) {
	cfg := testConfig(t)
	cfg.App.Env = "local"
	cfg.Payment.Midtrans.Environment = config.MidtransProduction
	_, err := newPaymentRegistrars(cfg, &Clients{})
	assert.ErrorContains(t, err, "simulator")
}
