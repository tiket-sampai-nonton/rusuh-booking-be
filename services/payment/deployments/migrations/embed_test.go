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
	assert.Len(t, migs, 6)
	assert.Contains(t, migs[4].Up, "uuid_generate_v7", "0005: outbox memakai UUIDv7 (ADR-032)")
	assert.Contains(t, migs[4].Down, "gen_random_uuid", "0005 down: kembali ke default v4")
	assert.Equal(t, 3, strings.Count(migs[5].Up, "SET DEFAULT uuid_generate_v7()"), "0006: tiga tabel sisa memakai UUIDv7")
	assert.Equal(t, 3, strings.Count(migs[5].Down, "SET DEFAULT gen_random_uuid()"), "0006 down: kembali ke default v4")
	for _, m := range migs {
		assert.NotContains(t, m.Up, "search_path", "search_path diatur runner, bukan file migrasi")
		assert.NotContains(t, m.Up, "COMMIT;", "runner sudah membungkus migrasi dalam transaksi")
	}
}
