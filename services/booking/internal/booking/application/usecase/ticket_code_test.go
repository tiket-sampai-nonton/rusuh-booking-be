package usecase

import (
	"fmt"
	"math/rand"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestTicketCodeFor_IsStableLongEnoughAndUnique(t *testing.T) {
	code := ticketCodeFor("11111111-1111-1111-1111-111111111111")
	item := "a3b4c5d6-e7f8-4a9b-8c7d-0123456789ab"
	assert.Equal(t, "TK-A3B4C5D6E7F8", code(item), "12 karakter hex, huruf besar, tanpa tanda hubung")
	assert.Equal(t, code(item), code(item), "deterministik")
	assert.Equal(t, "TK-ABC", code("abc"), "id pendek tidak menyebabkan panic")
	assert.Equal(t, "TK-111111111111", code(""), "tanpa id item memakai id booking")

	// Tiket dengan 8 karakter awal yang sama (tabrakan lama) kini berbeda.
	a, b := "a3b4c5d6-0000-4000-8000-000000000001", "a3b4c5d6-1111-4000-8000-000000000002"
	assert.NotEqual(t, code(a), code(b))
	// 20.000 id acak seperti UUID v4 (seed tetap agar test deterministik): tidak ada kode yang sama.
	r := rand.New(rand.NewSource(1)) // #nosec G404 -- test data only
	seen := map[string]bool{}
	for i := 0; i < 20000; i++ {
		id := fmt.Sprintf("%08x-%04x-4%03x-8%03x-%012x", r.Uint32(), r.Intn(1<<16), r.Intn(1<<12), r.Intn(1<<12), r.Int63n(1<<48))
		c := code(id)
		assert.False(t, seen[c], "kode %s bertabrakan", c)
		seen[c] = true
	}
}
