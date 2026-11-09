package valueobject_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	vo "github.com/tiket-sampai-nonton/rusuh-booking-be/services/payment/internal/payment/domain/valueobject"
)

func TestParseScenario(t *testing.T) {
	tests := []struct {
		in   string
		want vo.Scenario
		ok   bool
	}{
		{"SETTLE", vo.ScenarioSettle, true},
		{"SETTLE_LATE", vo.ScenarioSettleLate, true},
		{"EXPIRE", vo.ScenarioExpire, true},
		{"DENY", vo.ScenarioDeny, true},
		{"DUPLICATE_WEBHOOK", vo.ScenarioDuplicateWebhook, true},
		{"OUT_OF_ORDER", vo.ScenarioOutOfOrder, true},
		{"TIMEOUT", vo.ScenarioTimeout, true},
		{"", "", false},
		{"settle", "", false},
		{"REFUND", "", false},
		{" SETTLE", "", false},
	}
	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			got, ok := vo.ParseScenario(tc.in)
			assert.Equal(t, tc.ok, ok)
			assert.Equal(t, tc.want, got)
		})
	}
}
