package valueobject

// Scenario is a local-simulator payment scenario (payment.yaml SimulatorRequest.scenario).
type Scenario string

// Simulator scenarios.
const (
	ScenarioSettle           Scenario = "SETTLE"
	ScenarioSettleLate       Scenario = "SETTLE_LATE"
	ScenarioExpire           Scenario = "EXPIRE"
	ScenarioDeny             Scenario = "DENY"
	ScenarioDuplicateWebhook Scenario = "DUPLICATE_WEBHOOK"
	ScenarioOutOfOrder       Scenario = "OUT_OF_ORDER"
	ScenarioTimeout          Scenario = "TIMEOUT"
)

// ParseScenario returns the scenario for s (exact, case-sensitive), or false when s is unknown.
func ParseScenario(s string) (Scenario, bool) {
	switch sc := Scenario(s); sc {
	case ScenarioSettle, ScenarioSettleLate, ScenarioExpire, ScenarioDeny,
		ScenarioDuplicateWebhook, ScenarioOutOfOrder, ScenarioTimeout:
		return sc, true
	}
	return "", false
}
