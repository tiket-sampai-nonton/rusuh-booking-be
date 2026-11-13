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
	assert.Len(t, migs, 14)
	m12 := migs[11]
	assert.Equal(t, 7, strings.Count(m12.Up, "SET DEFAULT uuid_generate_v7()"), "0012: id baru memakai UUIDv7 (ADR-032)")
	assert.Equal(t, 7, strings.Count(m12.Down, "SET DEFAULT gen_random_uuid()"), "0012 down: kembali ke default v4")
	m13 := migs[12]
	assert.Contains(t, m13.Up, "now() >= OLD.sales_starts_at", "0013: aturan terkunci saat jendela penjualan dibuka")
	assert.Contains(t, m13.Down, "IF NEW.status = 'AVAILABLE' AND OLD.rules_locked_at IS NULL", "0013 down: guard 0001 dikembalikan")
	last := migs[len(migs)-1]
	assert.Contains(t, last.Up, "ADD COLUMN sweep_lease_until timestamptz", "0014: lease penyapu di admissions dan bookings")
	assert.Equal(t, 2, strings.Count(last.Up, "ADD COLUMN sweep_lease_until"), "0014: kedua tabel")
	assert.Equal(t, 2, strings.Count(last.Down, "DROP COLUMN sweep_lease_until"), "0014 down: kedua kolom dihapus")
	for _, m := range migs {
		assert.NotContains(t, m.Up, "search_path", "search_path diatur runner, bukan file migrasi")
		assert.NotContains(t, m.Up, "COMMIT;", "runner sudah membungkus migrasi dalam transaksi")
	}
}
