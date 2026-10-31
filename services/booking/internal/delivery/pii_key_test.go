package delivery

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildPII_DevKeyOnlyForLocalRuns(t *testing.T) {
	for _, env := range []string{"", "local"} {
		t.Setenv("APP_ENV", env)
		t.Setenv("PII_HASH_KEY", "")
		t.Setenv("PII_ENC_KEY", "")
		_, err := buildPII()
		assert.NoError(t, err, "APP_ENV=%q boleh memakai kunci dev", env)
	}
	for _, env := range []string{"dev", "staging", "prod"} {
		t.Setenv("APP_ENV", env)
		t.Setenv("PII_HASH_KEY", "")
		t.Setenv("PII_ENC_KEY", "")
		_, err := buildPII()
		require.Error(t, err, "APP_ENV=%s tanpa kunci harus gagal start", env)
		assert.Contains(t, err.Error(), "wajib diisi")
	}
	t.Setenv("APP_ENV", "prod")
	t.Setenv("PII_HASH_KEY", "101112131415161718191a1b1c1d1e1f000102030405060708090a0b0c0d0e0f")
	t.Setenv("PII_ENC_KEY", "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f")
	_, err := buildPII()
	assert.NoError(t, err)
}
