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
	// 0001 init, 0002 account_ref, 0003 anonymous_audit, 0004 event_settings_audit, 0005 poll_thresholds, 0006 sale window.
	// The old opening_window migration was dropped with the opening-window draw (ADR-030 Amendment);
	// it was uncommitted/unapplied so it is deleted, not reversed by a down migration (design-change §5).
	assert.Len(t, migs, 6)
	assert.Contains(t, migs[3].Up, "event_settings_audit_immutable", "audit settings append-only di level DB")
	assert.Contains(t, migs[3].Down, "DROP TABLE event_settings_audit")
	assert.Contains(t, migs[4].Up, "poll_far_above", "threshold polling ditambah via migration 0005")
	assert.Contains(t, migs[4].Down, "poll_far_above")
	assert.Contains(t, migs[5].Up, "sales_ends_at", "sale window dicatat untuk iterasi worker (C8.10) lewat migrasi 0006")
	assert.Contains(t, migs[5].Down, "DROP COLUMN sales_ends_at")
	for _, m := range migs {
		assert.NotContains(t, m.Up, "search_path", "search_path diatur runner, bukan file migrasi")
		assert.NotContains(t, m.Up, "COMMIT;", "runner sudah membungkus migrasi dalam transaksi")
	}
}
