package delivery

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/user/internal/config"
)

func testConfig(t *testing.T) config.Config {
	t.Helper()

	t.Setenv("TOKEN_PRIVATE_KEY_FILE", writeTestKey(t))
	t.Setenv("PII_ENC_KEY", "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f")
	t.Setenv("PII_HASH_KEY", "101112131415161718191a1b1c1d1e1f000102030405060708090a0b0c0d0e0f")
	t.Setenv("OTP_HASH_KEY", "202122232425262728292a2b2c2d2e2f000102030405060708090a0b0c0d0e0f")
	cfg, err := config.Load()
	require.NoError(t, err)
	return cfg
}
