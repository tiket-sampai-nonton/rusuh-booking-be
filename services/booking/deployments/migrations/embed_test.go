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
	assert.Len(t, migs, 12)
	last := migs[len(migs)-1]
	assert.Equal(t, 7, strings.Count(last.Up, "SET DEFAULT uuid_generate_v7()"), "0012: id baru memakai UUIDv7 (ADR-032)")
	assert.Equal(t, 7, strings.Count(last.Down, "SET DEFAULT gen_random_uuid()"), "0012 down: kembali ke default v4")
	for _, m := range migs {
		assert.NotContains(t, m.Up, "search_path", "search_path diatur runner, bukan file migrasi")
		assert.NotContains(t, m.Up, "COMMIT;", "runner sudah membungkus migrasi dalam transaksi")
	}
}
