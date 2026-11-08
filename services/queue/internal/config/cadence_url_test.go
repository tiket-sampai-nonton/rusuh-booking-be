package config

import (
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TC-QUE-31: job intervals default and are validated; each error names its variable.
func TestLoad_JobIntervalDefaults(t *testing.T) {
	setBase(t)
	cfg, err := Load()
	require.NoError(t, err)
	assert.Equal(t, 100*time.Millisecond, cfg.WorkerTuning.PromotorInterval)
	assert.Equal(t, 10*time.Second, cfg.WorkerTuning.SweeperInterval)
	assert.Equal(t, time.Second, cfg.WorkerTuning.StatusInterval)
}

func TestLoad_JobIntervalsInvalid(t *testing.T) {
	vars := []string{"QUEUE_PROMOTOR_INTERVAL_MS", "QUEUE_SWEEPER_INTERVAL_MS", "QUEUE_STATUS_INTERVAL_MS", "WORKER_INTERVAL"}
	for _, v := range vars {
		for _, val := range []string{"0s", "-1s", "abc"} {
			t.Run(v+"="+val, func(t *testing.T) {
				setBase(t)
				t.Setenv(v, val)
				_, err := Load()
				require.Error(t, err)
				assert.ErrorContains(t, err, v)
				for _, other := range vars {
					if other != v {
						assert.NotContains(t, err.Error(), other)
					}
				}
			})
		}
	}
}

func TestLoad_JobIntervalsTwoInvalid(t *testing.T) {
	setBase(t)
	t.Setenv("QUEUE_PROMOTOR_INTERVAL_MS", "0s")
	t.Setenv("QUEUE_STATUS_INTERVAL_MS", "-5s")
	_, err := Load()
	require.Error(t, err)
	assert.ErrorContains(t, err, "QUEUE_PROMOTOR_INTERVAL_MS")
	assert.ErrorContains(t, err, "QUEUE_STATUS_INTERVAL_MS")
}

// TC-QUE-34: BOOKING_PAGE_BASE_URL validation.
func TestLoad_PageBaseURL(t *testing.T) {
	tests := []struct {
		name, env, val string
		unset          bool
		want           string // "" = valid
		wantStored     string
	}{
		{name: "https ok", env: "prod", val: "https://app.example.com/book", wantStored: "https://app.example.com/book"},
		{name: "http local ok", env: "local", val: "http://localhost:3000/book", wantStored: "http://localhost:3000/book"},
		{name: "trailing slash trimmed", env: "prod", val: "https://app.example.com/book/", wantStored: "https://app.example.com/book"},
		{name: "http dev rejected", env: "dev", val: "http://app.example.com/book", want: "https"},
		{name: "http prod rejected", env: "prod", val: "http://app.example.com/book", want: "https"},
		{name: "missing", env: "local", unset: true, want: "wajib"},
		{name: "empty", env: "local", val: "", want: "wajib"},
		{name: "relative", env: "local", val: "/book", want: "BOOKING_PAGE_BASE_URL"},
		{name: "ftp", env: "local", val: "ftp://x.test/book", want: "http"},
		{name: "javascript", env: "local", val: "javascript:alert(1)", want: "BOOKING_PAGE_BASE_URL"},
		{name: "no host", env: "local", val: "https:///book", want: "host"},
		{name: "query", env: "local", val: "https://x.test/book?a=1", want: "query"},
		{name: "empty query", env: "local", val: "https://x.test/book?", want: "query"},
		{name: "fragment", env: "local", val: "https://x.test/book#f", want: "fragment"},
		{name: "empty fragment", env: "local", val: "https://x.test/book#", want: "fragment"},
		{name: "userinfo", env: "local", val: "https://u:p@x.test/book", want: "userinfo"},
		{name: "whitespace", env: "local", val: "https://x.test/bo ok", want: "spasi"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setBase(t)
			t.Setenv("APP_ENV", tt.env)
			if tt.unset {
				// t.Setenv registered cleanup; Unsetenv for the duration of this test.
				t.Setenv("BOOKING_PAGE_BASE_URL", "x")
				require.NoError(t, os.Unsetenv("BOOKING_PAGE_BASE_URL"))
			} else {
				t.Setenv("BOOKING_PAGE_BASE_URL", tt.val)
			}
			t.Setenv("BOOKING_SERVICE_URL", "http://internal-secret-host:8082")
			cfg, err := Load()
			if tt.want == "" && tt.wantStored != "" {
				require.NoError(t, err)
				assert.Equal(t, tt.wantStored, cfg.Booking.PageBaseURL)
				return
			}
			require.Error(t, err)
			assert.ErrorContains(t, err, "BOOKING_PAGE_BASE_URL")
			assert.ErrorContains(t, err, tt.want)
			assert.NotContains(t, err.Error(), "internal-secret-host")
		})
	}
}

func TestLoad_PageBaseURLReportedWithOthers(t *testing.T) {
	setBase(t)
	t.Setenv("BOOKING_PAGE_BASE_URL", "ftp://x.test")
	t.Setenv("APP_PORT", "70000")
	_, err := Load()
	require.Error(t, err)
	assert.ErrorContains(t, err, "BOOKING_PAGE_BASE_URL")
	assert.ErrorContains(t, err, "APP_PORT")
}

func TestLoad_SeparateIntervalMessages(t *testing.T) {
	setBase(t)
	t.Setenv("WORKER_INTERVAL", "0s")
	_, err := Load()
	require.Error(t, err)
	assert.ErrorContains(t, err, "WORKER_INTERVAL")
	assert.NotContains(t, err.Error(), "HEALTH_TIMEOUT")
	assert.NotContains(t, err.Error(), "HTTP_SHUTDOWN_TIMEOUT")
}

// The env reader trims surrounding blanks, so leading whitespace is checked on the validator directly.
func TestValidatePageBaseURL_RejectsSurroundingWhitespace(t *testing.T) {
	assert.ErrorContains(t, validatePageBaseURL(" https://x.test/book", "local"), "spasi")
	assert.ErrorContains(t, validatePageBaseURL("https://x.test/book\n", "local"), "spasi")
}
