package smtp

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRedact_HidesRecipientAddressButKeepsCause(t *testing.T) {
	cause := fmt.Errorf("smtp: rcpt: %w", errors.New("550 5.1.1 <budi.santoso+tiket@contoh.co.id>: Recipient address rejected"))
	got := redact(cause)

	assert.NotContains(t, got.Error(), "budi")
	assert.NotContains(t, got.Error(), "@")
	assert.Contains(t, got.Error(), "[email]")
	assert.Contains(t, got.Error(), "Recipient address rejected", "alasan error tetap terbaca untuk diagnosis")
	assert.ErrorIs(t, got, cause, "rantai error dipertahankan")
}

func TestRedact_LeavesErrorsWithoutAddressUntouched(t *testing.T) {
	cause := errors.New("smtp: dial localhost:1025: connection refused")
	assert.Same(t, cause, redact(cause))
}

func TestRedact_KeepsContextErrors(t *testing.T) {
	assert.ErrorIs(t, redact(fmt.Errorf("x: %w", context.DeadlineExceeded)), context.DeadlineExceeded)
}
