package valueobject_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	vo "github.com/tiket-sampai-nonton/rusuh-booking-be/services/payment/internal/payment/domain/valueobject"
)

func TestMapProviderStatus(t *testing.T) {
	cases := []struct {
		status, fraud string
		want          vo.AttemptStatus
		ok            bool
	}{
		{"pending", "", vo.AttemptPending, true},
		{"settlement", "accept", vo.AttemptSucceeded, true},
		{"settlement", "", vo.AttemptSucceeded, true},
		{"capture", "accept", vo.AttemptSucceeded, true},
		{"capture", "deny", "", false},
		{"deny", "", vo.AttemptFailed, true},
		{"failure", "", vo.AttemptFailed, true},
		{"cancel", "", vo.AttemptCancelled, true},
		{"expire", "", vo.AttemptExpired, true},
		{"refund", "", "", false},
		{"partial_refund", "", "", false},
		{"chargeback", "", "", false},
		{"authorize", "", "", false},
		{"", "", "", false},
		{"SETTLEMENT", "", "", false},
	}
	for _, c := range cases {
		got, ok := vo.MapProviderStatus(c.status, c.fraud)
		assert.Equal(t, c.ok, ok, "%s/%s", c.status, c.fraud)
		assert.Equal(t, c.want, got, "%s/%s", c.status, c.fraud)
	}
}

func TestParseSettlementTime_ReadsWIBReturnsUTC(t *testing.T) {
	got, err := vo.ParseSettlementTime("2026-10-26 17:30:05")
	require.NoError(t, err)
	assert.Equal(t, time.Date(2026, 10, 26, 10, 30, 5, 0, time.UTC), got)
	assert.Equal(t, time.UTC, got.Location())

	for _, bad := range []string{"", "kemarin", "2026-10-26T17:30:05Z", "26-10-2026 17:30:05"} {
		_, err := vo.ParseSettlementTime(bad)
		assert.Error(t, err, bad)
	}
}
