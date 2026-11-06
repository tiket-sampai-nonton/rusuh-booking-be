package migrations

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/migrate"
)

func TestEmbeddedMigrationsLoad(t *testing.T) {
	migs, err := migrate.Load(FS, ".")
	require.NoError(t, err)
	// 0001 init, 0002 account_ref, 0003 anonymous_audit, 0004 event_settings_audit. The old opening_window migration was dropped
	// with the opening-window draw (ADR-030 Amendment); it was uncommitted/unapplied so it is deleted, not
	// reversed by a 0005 (design-change §5).
	assert.Len(t, migs, 4)
	assert.Contains(t, migs[3].Up, "event_settings_audit_immutable", "audit settings append-only di level DB")
	assert.Contains(t, migs[3].Down, "DROP TABLE event_settings_audit")
	for _, m := range migs {
		assert.NotContains(t, m.Up, "search_path", "search_path diatur runner, bukan file migrasi")
		assert.NotContains(t, m.Up, "COMMIT;", "runner sudah membungkus migrasi dalam transaksi")
	}
}
