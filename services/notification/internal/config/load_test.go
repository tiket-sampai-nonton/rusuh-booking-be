package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setBase clears variables that tests read, so results do not depend on the developer's shell
// (an empty value counts as unset for the loader).
func setBase(t *testing.T) {
	t.Helper()
	for _, k := range []string{"EMAIL_PROVIDER", "EMAIL_FROM", "SMTP_FROM", "RESEND_API_KEY"} {
		t.Setenv(k, "")
	}
}

func TestLoad_Defaults(t *testing.T) {
	setBase(t)
	cfg, err := Load()
	require.NoError(t, err)
	assert.Equal(t, 8084, cfg.App.Port)
	assert.Equal(t, "local", cfg.App.Env)
	assert.Equal(t, "notification", cfg.Postgres.Schema)
	assert.Equal(t, "svc_notification", cfg.Postgres.User)
	assert.Equal(t, "rusuh.events", cfg.RabbitMQ.Exchange)
	assert.Len(t, cfg.RabbitMQ.RetryDelays, 3)
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
		{"bad delays", "RABBITMQ_RETRY_DELAYS", "soon", "RABBITMQ_RETRY_DELAYS"},
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

func TestLoad_EmailProviderDefaultsToMailpit(t *testing.T) {
	setBase(t)
	cfg, err := Load()
	require.NoError(t, err)
	assert.Equal(t, "mailpit", cfg.Email.Provider)
	assert.Empty(t, cfg.Email.ResendAPIKey)
}

func TestLoad_EmailProvider(t *testing.T) {
	tests := []struct {
		name    string
		env     map[string]string
		wantErr string // empty = valid
	}{
		{"mailpit explicit", map[string]string{"EMAIL_PROVIDER": "mailpit"}, ""},
		{"unknown provider", map[string]string{"EMAIL_PROVIDER": "ses"}, "EMAIL_PROVIDER"},
		{"resend without key", map[string]string{"EMAIL_PROVIDER": "resend", "EMAIL_FROM": "no-reply@rusuh.dev"}, "RESEND_API_KEY"},
		{"resend without from", map[string]string{"EMAIL_PROVIDER": "resend", "RESEND_API_KEY": "re_test", "EMAIL_FROM": "", "SMTP_FROM": ""}, "EMAIL_FROM"},
		{"resend complete", map[string]string{"EMAIL_PROVIDER": "resend", "RESEND_API_KEY": "re_test", "EMAIL_FROM": "no-reply@rusuh.dev"}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setBase(t)
			for k, v := range tt.env {
				t.Setenv(k, v)
			}
			cfg, err := Load()
			if tt.wantErr != "" {
				assert.ErrorContains(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.env["EMAIL_PROVIDER"], cfg.Email.Provider)
		})
	}
}

func TestLoad_EmailErrorNeverEchoesKey(t *testing.T) {
	setBase(t)
	t.Setenv("EMAIL_PROVIDER", "ses") // invalid on purpose
	t.Setenv("RESEND_API_KEY", "re_super_secret_value")
	_, err := Load()
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "re_super_secret_value")
}

func TestLoad_EmailFromFallsBackToSMTPFrom(t *testing.T) {
	setBase(t)
	t.Setenv("EMAIL_FROM", "")
	t.Setenv("SMTP_FROM", "legacy@rusuh.test")
	cfg, err := Load()
	require.NoError(t, err)
	assert.Equal(t, "legacy@rusuh.test", cfg.Email.From)

	t.Setenv("EMAIL_FROM", "new@rusuh.dev")
	cfg, err = Load()
	require.NoError(t, err)
	assert.Equal(t, "new@rusuh.dev", cfg.Email.From)
}
