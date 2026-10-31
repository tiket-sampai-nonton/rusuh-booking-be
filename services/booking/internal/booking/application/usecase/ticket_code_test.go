package usecase

import (
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
)

// The ticket code is the QR payload: it must be long, random, and not derivable from anything the buyer sees.
func TestNewTicketCode_IsRandomAndLongEnough(t *testing.T) {
	re := regexp.MustCompile(`^TK-[0-9A-F]{20}$`)
	seen := map[string]bool{}
	for i := 0; i < 20000; i++ {
		c := NewTicketCode()
		assert.Regexp(t, re, c, "80 bit acak, huruf besar")
		assert.False(t, seen[c], "kode %s bertabrakan", c)
		seen[c] = true
	}
}
