package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/migrate"
)

func TestEmbeddedMigrationsLoad(t *testing.T) {
	migs, err := migrate.Load(FS, ".")
	require.NoError(t, err)
	assert.Len(t, migs, 13)
	m12 := migs[11]
	assert.Equal(t, 7, strings.Count(m12.Up, "SET DEFAULT uuid_generate_v7()"), "0012: id baru memakai UUIDv7 (ADR-032)")
	assert.Equal(t, 7, strings.Count(m12.Down, "SET DEFAULT gen_random_uuid()"), "0012 down: kembali ke default v4")
	last := migs[len(migs)-1]
	assert.Contains(t, last.Up, "now() >= OLD.sales_starts_at", "0013: aturan terkunci saat jendela penjualan dibuka")
	assert.Contains(t, last.Down, "IF NEW.status = 'AVAILABLE' AND OLD.rules_locked_at IS NULL", "0013 down: guard 0001 dikembalikan")
	for _, m := range migs {
		assert.NotContains(t, m.Up, "search_path", "search_path diatur runner, bukan file migrasi")
		assert.NotContains(t, m.Up, "COMMIT;", "runner sudah membungkus migrasi dalam transaksi")
	}
}
