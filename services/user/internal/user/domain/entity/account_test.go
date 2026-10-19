package entity

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestAccountAndSessionHelpers(t *testing.T) {
	assert.True(t, Account{Status: AccountActive}.Active())
	assert.False(t, Account{Status: AccountDisabled}.Active())
	assert.True(t, Account{NIKMasked: "3174********0001"}.HasNIK())
	assert.False(t, Account{}.HasNIK())

	now := time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC)
	s := Session{ExpiresAt: now.Add(time.Hour)}
	assert.False(t, s.Expired(now))
	assert.True(t, s.Expired(now.Add(time.Hour)))
	assert.False(t, s.Revoked())
	s.RevokedAt = &now
	assert.True(t, s.Revoked())
}
