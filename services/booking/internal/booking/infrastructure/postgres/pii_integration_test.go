//go:build integration

package postgres

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// PDP-1b: no plaintext buyer email column is left in Booking; only the ciphertext one (written by Queue) remains.
func TestBuyerEmail_OnlyTheCiphertextColumnExists(t *testing.T) {
	f := seed(t)
	cols := func(table string) int {
		return f.n(t, `SELECT count(*) FROM information_schema.columns WHERE table_schema='booking' AND table_name=$1 AND column_name='buyer_email'`, table)
	}
	assert.Equal(t, 0, cols("admissions"))
	assert.Equal(t, 0, cols("bookings"))
	assert.Equal(t, 1, f.n(t, `SELECT count(*) FROM information_schema.columns WHERE table_schema='booking' AND table_name='bookings' AND column_name='buyer_email_enc'`))
}
