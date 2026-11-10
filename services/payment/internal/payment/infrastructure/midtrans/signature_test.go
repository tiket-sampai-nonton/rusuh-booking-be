package midtrans_test

import (
	"crypto/sha512"
	"encoding/hex"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/payment/internal/payment/infrastructure/midtrans"
)

func sign(orderID, code, amount, key string) string {
	sum := sha512.Sum512([]byte(orderID + code + amount + key))
	return hex.EncodeToString(sum[:])
}

// Property 3: Signature is the only proof.
func TestSignatureVerifier(t *testing.T) {
	v := midtrans.NewSignatureVerifier(serverKey)
	good := sign("RB-1-1", "200", "1500000.00", serverKey)

	assert.True(t, v.Verify("RB-1-1", "200", "1500000.00", good))
	assert.True(t, v.Verify("RB-1-1", "200", "1500000.00", upper(good)), "hex huruf besar tetap valid")

	assert.False(t, v.Verify("RB-1-2", "200", "1500000.00", good), "order id berbeda")
	assert.False(t, v.Verify("RB-1-1", "201", "1500000.00", good), "status code berbeda")
	assert.False(t, v.Verify("RB-1-1", "200", "1.00", good), "nominal berbeda")
	assert.False(t, v.Verify("RB-1-1", "200", "1500000.00", sign("RB-1-1", "200", "1500000.00", "kunci-lain")), "server key lain")
	assert.False(t, v.Verify("RB-1-1", "200", "1500000.00", ""))
	assert.False(t, v.Verify("RB-1-1", "200", "1500000.00", good[:len(good)-1]))
	assert.False(t, midtrans.NewSignatureVerifier("").Verify("RB-1-1", "200", "1500000.00", sign("RB-1-1", "200", "1500000.00", "")),
		"server key kosong tidak pernah memverifikasi")
}

func upper(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'a' && c <= 'f' {
			b[i] = c - 32
		}
	}
	return string(b)
}
