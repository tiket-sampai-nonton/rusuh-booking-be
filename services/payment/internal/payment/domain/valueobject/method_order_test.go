package valueobject_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	vo "github.com/tiket-sampai-nonton/rusuh-booking-be/services/payment/internal/payment/domain/valueobject"
)

func TestParseMethod(t *testing.T) {
	for _, s := range []string{"VA_BCA", "VA_BNI", "VA_BRI", "VA_PERMATA", "QRIS", "GOPAY"} {
		m, ok := vo.ParseMethod(s)
		assert.True(t, ok, s)
		assert.Equal(t, s, string(m))
	}
	for _, s := range []string{"", "CREDIT_CARD", "va_bca", "VA_BCA "} {
		_, ok := vo.ParseMethod(s)
		assert.False(t, ok, "%q", s)
	}
	assert.True(t, vo.MethodVAPermata.IsVirtualAccount())
	assert.False(t, vo.MethodQRIS.IsVirtualAccount())
	assert.False(t, vo.MethodGoPay.IsVirtualAccount())
}

func TestNewOrderID(t *testing.T) {
	id, err := vo.NewOrderID("RB-7F3A9C21", 2)
	require.NoError(t, err)
	assert.Equal(t, "RB-7F3A9C21-2", id)

	for name, tc := range map[string]struct {
		ref string
		no  int
	}{
		"ref kosong": {"", 1}, "attempt 0": {"RB-1", 0}, "karakter terlarang": {"RB 1/x", 1},
		"terlalu panjang": {"RB-" + strings.Repeat("A", 60), 1},
	} {
		_, err := vo.NewOrderID(tc.ref, tc.no)
		assert.ErrorIs(t, err, vo.ErrOrderID, name)
	}
}
