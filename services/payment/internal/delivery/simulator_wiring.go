package delivery

import (
	"fmt"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/payment/internal/config"
)

// simulatorEnabled reports whether the local payment simulator may exist. Only local and ci: in dev, staging
// and prod neither the adapter, the usecase nor the route is constructed.
func simulatorEnabled(appEnv string) bool { return appEnv == "local" || appEnv == "ci" }

// checkSimulatorEnv is a second line of defence: a local-like environment must never carry the production
// Midtrans configuration while the simulator can inject notifications.
func checkSimulatorEnv(appEnv, midtransEnv string) error {
	if simulatorEnabled(appEnv) && midtransEnv == config.MidtransProduction {
		return fmt.Errorf("payment: simulator aktif di APP_ENV=%s tetapi MIDTRANS_ENVIRONMENT=%s; tolak start", appEnv, midtransEnv)
	}
	return nil
}
