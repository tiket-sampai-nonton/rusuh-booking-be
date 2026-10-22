package delivery

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/notification/internal/config"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/notification/internal/notification/infrastructure/resend"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/notification/internal/notification/infrastructure/smtp"
)

const secretKey = "re_super_secret_key"

func logTo(buf *bytes.Buffer) *slog.Logger { return slog.New(slog.NewTextHandler(buf, nil)) }

func emailCfg(provider string) config.Config {
	return config.Config{
		Email: config.Email{Provider: provider, From: "RBT <no-reply@rusuh.dev>", ResendAPIKey: secretKey},
		SMTP:  config.SMTP{Host: "localhost", Port: 1025},
	}
}

// Property 7: mailpit never uses the Resend adapter, even when a Resend key is present in the env.
func TestNewEmailSender_MailpitUsesSMTP(t *testing.T) {
	var buf bytes.Buffer
	s, err := newEmailSender(emailCfg(config.EmailMailpit), logTo(&buf))
	require.NoError(t, err)
	assert.IsType(t, &smtp.Sender{}, s)
	assert.Empty(t, buf.String(), "mailpit mode must not warn")
}

func TestNewEmailSender_ResendWarnsOnceAndHidesKey(t *testing.T) {
	var buf bytes.Buffer
	s, err := newEmailSender(emailCfg(config.EmailResend), logTo(&buf))
	require.NoError(t, err)
	assert.IsType(t, &resend.Sender{}, s)
	assert.Equal(t, 1, strings.Count(buf.String(), "EMAIL_PROVIDER=resend"), "exactly one start-up warning")
	assert.NotContains(t, buf.String(), secretKey)
}

func TestNewEmailSender_UnknownProviderFails(t *testing.T) {
	_, err := newEmailSender(emailCfg("ses"), logTo(&bytes.Buffer{}))
	require.Error(t, err)
	assert.NotContains(t, err.Error(), secretKey)
}
