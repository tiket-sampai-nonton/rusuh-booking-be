package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setBase(t *testing.T) {
	t.Helper()
	t.Setenv("TOKEN_PRIVATE_KEY_FILE", "/run/secrets/queue-token.pem")
	t.Setenv("BOOKING_SERVICE_URL", "http://booking:8082")
	t.Setenv("PII_ENC_KEY", "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f")
	t.Setenv("PII_HMAC_KEY", "101112131415161718191a1b1c1d1e1f000102030405060708090a0b0c0d0e0f")
}

func TestLoad_Defaults(t *testing.T) {
	setBase(t)
	cfg, err := Load()
	require.NoError(t, err)
	assert.Equal(t, 8081, cfg.App.Port)
	assert.Equal(t, "local", cfg.App.Env)
	assert.Equal(t, "queue", cfg.Postgres.Schema)
	assert.Equal(t, "svc_queue", cfg.Postgres.User)
	assert.Equal(t, "rusuh.events", cfg.RabbitMQ.Exchange)
	assert.Len(t, cfg.RabbitMQ.RetryDelays, 3)
	assert.Equal(t, 0, cfg.Valkey.DB)
}

func TestLoad_QueueExtras(t *testing.T) {
	setBase(t)
	t.Setenv("TOKEN_ADMISSION_TTL", "60s")
	t.Setenv("QUEUE_PROMOTOR_INTERVAL_MS", "100ms")
	t.Setenv("QUEUE_PROMOTE_BATCH_MAX", "200")
	t.Setenv("RATE_LIMIT_JOIN_PER_EMAIL", "5")
	cfg, err := Load()
	require.NoError(t, err)
	assert.Equal(t, "http://booking:8082", cfg.Booking.BaseURL)
	assert.Equal(t, 200, cfg.WorkerTuning.PromoteBatchMax)
	assert.Equal(t, 5, cfg.RateLimit.JoinPerEmail)
	assert.Equal(t, int64(100), cfg.WorkerTuning.PromotorInterval.Milliseconds())
	assert.Equal(t, int64(60), int64(cfg.Token.AdmissionTTL.Seconds()))
}

func TestConfigValidate_TokenPII(t *testing.T) {
	// Missing TOKEN_PRIVATE_KEY_FILE, bad PII keys, and missing BOOKING_SERVICE_URL aggregate.
	t.Setenv("PII_ENC_KEY", "00ff")
	t.Setenv("PII_HMAC_KEY", "")
	_, err := Load()
	require.Error(t, err)
	assert.ErrorContains(t, err, "TOKEN_PRIVATE_KEY_FILE")
	assert.ErrorContains(t, err, "PII_ENC_KEY")
	assert.ErrorContains(t, err, "BOOKING_SERVICE_URL")
}

func TestLoad_ReportsInvalidValues(t *testing.T) {
	tests := []struct {
		name, key, value, want string
	}{
		{"bad port", "APP_PORT", "eighty", "APP_PORT"},
		{"port range", "APP_PORT", "70000", "APP_PORT"},
		{"bad env", "APP_ENV", "qa", "APP_ENV"},
		{"bad level", "LOG_LEVEL", "verbose", "LOG_LEVEL"},
		{"bad timeout", "HEALTH_TIMEOUT", "0s", "HEALTH_TIMEOUT"},
		{"bad schema", "POSTGRES_SCHEMA", "public;drop", "POSTGRES_SCHEMA"},
		{"bad valkey", "VALKEY_SERVER", "localhost:6379", "VALKEY_SERVER"},
		{"bad delays", "RABBITMQ_RETRY_DELAYS", "soon", "RABBITMQ_RETRY_DELAYS"},
		{"short pii key", "PII_ENC_KEY", "00ff", "PII_ENC_KEY"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setBase(t)
			t.Setenv(tt.key, tt.value)
			_, err := Load()
			assert.ErrorContains(t, err, tt.want)
		})
	}
}

// QSH-T08: new worker knobs are loaded with defaults and validated at start (D-24); each error names its env var.
func TestLoad_QueueHardeningDefaults(t *testing.T) {
	setBase(t)
	cfg, err := Load()
	require.NoError(t, err)
	assert.Equal(t, 10*time.Second, cfg.WorkerTuning.PromoterLeaseTTL)
	assert.Equal(t, 500, cfg.WorkerTuning.SweepBatchMax)
	assert.Equal(t, 3, cfg.WorkerTuning.UnsealFailMax)
	assert.Equal(t, 3, cfg.WorkerTuning.UnsealProbe)
	assert.Equal(t, 50, cfg.WorkerTuning.NoSupplySuspectTicks)
	// REQ-5: status-doc TTL multiplier defaults to 4 (within the 3..5 bound).
	assert.Equal(t, 4, cfg.WorkerTuning.StatusTTLMultiplier)
}

// REQ-5 AC7: StatusTTL returns the configured multiple of the effective publish cadence (Worker.Interval),
// computed by shipped production code — never a single interval.
func TestWorkerTuning_StatusTTL(t *testing.T) {
	tests := []struct {
		name     string
		mult     int
		interval time.Duration
		want     time.Duration
	}{
		{name: "min multiplier 3", mult: 3, interval: 5 * time.Second, want: 15 * time.Second},
		{name: "default multiplier 4", mult: 4, interval: 5 * time.Second, want: 20 * time.Second},
		{name: "max multiplier 5", mult: 5, interval: 5 * time.Second, want: 25 * time.Second},
		{name: "different cadence", mult: 4, interval: 2 * time.Second, want: 8 * time.Second},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := WorkerTuning{StatusTTLMultiplier: tt.mult}
			got := w.StatusTTL(tt.interval)
			assert.Equal(t, tt.want, got)
			assert.NotEqual(t, tt.interval, got, "TTL must never equal a single publish interval")
			assert.Greater(t, got, tt.interval, "TTL must outlive the publish cadence")
		})
	}
}

// REQ-5 AC1/AC5: the multiplier is validated at start-up; a value outside 3..5 is rejected and names its env var.
func TestLoad_StatusTTLMultiplierValidation(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		want string // empty = valid
	}{
		{"multiplier 2 rejected (TTL would be too short vs cadence)", map[string]string{"QUEUE_STATUS_TTL_MULTIPLIER": "2"}, "QUEUE_STATUS_TTL_MULTIPLIER"},
		{"multiplier 3 ok (lower bound)", map[string]string{"QUEUE_STATUS_TTL_MULTIPLIER": "3"}, ""},
		{"multiplier 4 ok (default)", map[string]string{"QUEUE_STATUS_TTL_MULTIPLIER": "4"}, ""},
		{"multiplier 5 ok (upper bound)", map[string]string{"QUEUE_STATUS_TTL_MULTIPLIER": "5"}, ""},
		{"multiplier 6 rejected (TTL too long, stale risk)", map[string]string{"QUEUE_STATUS_TTL_MULTIPLIER": "6"}, "QUEUE_STATUS_TTL_MULTIPLIER"},
		{"multiplier 0 rejected", map[string]string{"QUEUE_STATUS_TTL_MULTIPLIER": "0"}, "QUEUE_STATUS_TTL_MULTIPLIER"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setBase(t)
			for k, v := range tt.env {
				t.Setenv(k, v)
			}
			_, err := Load()
			if tt.want == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.ErrorContains(t, err, tt.want)
		})
	}
}

func TestLoad_QueueHardeningValidation(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		want string // empty = valid
	}{
		{"lease ttl ok", map[string]string{"QUEUE_PROMOTER_LEASE_TTL_MS": "7s"}, ""},
		{"lease ttl at upper bound", map[string]string{"QUEUE_PROMOTER_LEASE_TTL_MS": "30s"}, ""},
		{"lease ttl below 2s", map[string]string{"QUEUE_PROMOTER_LEASE_TTL_MS": "1s"}, "QUEUE_PROMOTER_LEASE_TTL_MS"},
		{"lease ttl above 30s", map[string]string{"QUEUE_PROMOTER_LEASE_TTL_MS": "31s"}, "QUEUE_PROMOTER_LEASE_TTL_MS"},
		{"lease ttl not above 2x booking timeout", map[string]string{"BOOKING_TIMEOUT": "5s", "QUEUE_PROMOTER_LEASE_TTL_MS": "10s"}, "QUEUE_PROMOTER_LEASE_TTL_MS"},
		{"sweep batch zero", map[string]string{"QUEUE_SWEEP_BATCH_MAX": "0"}, "QUEUE_SWEEP_BATCH_MAX"},
		{"sweep batch too large", map[string]string{"QUEUE_SWEEP_BATCH_MAX": "5001"}, "QUEUE_SWEEP_BATCH_MAX"},
		{"sweep batch at max", map[string]string{"QUEUE_SWEEP_BATCH_MAX": "5000"}, ""},
		{"unseal fail max zero", map[string]string{"QUEUE_UNSEAL_FAIL_MAX": "0"}, "QUEUE_UNSEAL_FAIL_MAX"},
		{"unseal probe zero", map[string]string{"QUEUE_UNSEAL_PROBE": "0"}, "QUEUE_UNSEAL_PROBE"},
		{"suspect ticks 1 rejected (a single race must never label)", map[string]string{"QUEUE_NO_SUPPLY_SUSPECT_TICKS": "1"}, "QUEUE_NO_SUPPLY_SUSPECT_TICKS"},
		{"suspect ticks 2 ok", map[string]string{"QUEUE_NO_SUPPLY_SUSPECT_TICKS": "2"}, ""},
		{"rate window under 1ms", map[string]string{"RATE_LIMIT_JOIN_PER_EMAIL_WINDOW": "500us"}, "RATE_LIMIT_JOIN_PER_EMAIL_WINDOW"},
		{"ip window zero", map[string]string{"RATE_LIMIT_JOIN_PER_IP_WINDOW": "0s"}, "RATE_LIMIT_JOIN_PER_IP_WINDOW"},
		{"me window zero", map[string]string{"RATE_LIMIT_ME_PER_VISITOR_WINDOW": "0s"}, "RATE_LIMIT_ME_PER_VISITOR_WINDOW"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setBase(t)
			for k, v := range tt.env {
				t.Setenv(k, v)
			}
			_, err := Load()
			if tt.want == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.ErrorContains(t, err, tt.want)
		})
	}
}
