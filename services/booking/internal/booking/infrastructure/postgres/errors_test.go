package postgres

import (
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/inbound"
)

func pgErr(code, constraint string) error {
	return &pgconn.PgError{Code: code, ConstraintName: constraint, Message: "x"}
}

// Database failures that are the caller's doing must reach the client as their own error, not as a 500.
func TestMapHoldErr(t *testing.T) {
	assert.NoError(t, mapHoldErr(nil))
	assert.ErrorIs(t, mapHoldErr(pgErr("23514", "buyer_quotas_check")), inbound.ErrBuyerQuota)
	assert.ErrorIs(t, mapHoldErr(pgErr("23514", "category_inventories_check")), inbound.ErrCategoryUnavailable)
	assert.ErrorIs(t, mapHoldErr(pgErr("23505", "uq_attendee_nik_per_event")), inbound.ErrNikRegistered)
	assert.ErrorIs(t, mapHoldErr(pgErr("23505", "uq_booking_item_nik_active")), inbound.ErrNikDuplicate)
	other := errors.New("koneksi putus")
	assert.Equal(t, other, mapHoldErr(other), "kesalahan lain diteruskan apa adanya")
	assert.Equal(t, pgErr("23505", "lain"), mapHoldErr(pgErr("23505", "lain")))
}

func TestMapEditErrAndEventWriteErr(t *testing.T) {
	assert.ErrorIs(t, mapEditErr(pgErr("23514", "admissions_edit_count")), inbound.ErrEditLimit)
	other := errors.New("boom")
	assert.Equal(t, other, mapEditErr(other))

	assert.ErrorIs(t, mapEventWriteErr(errors.New("EVENT_RULES_LOCKED event=1"), "op"), inbound.ErrRulesLocked)
	assert.ErrorIs(t, mapEventWriteErr(errors.New("SUPPLY_CATEGORY_MISMATCH x"), "op"), inbound.ErrSupplyMismatch)
	assert.ErrorIs(t, mapEventWriteErr(pgErr("22P02", ""), "op"), inbound.ErrNotFound)
	assert.ErrorContains(t, mapEventWriteErr(pgErr("23505", "events_slug_key"), "op"), "slug")
	wrapped := mapEventWriteErr(errors.New("lain"), "events: create")
	assert.ErrorContains(t, wrapped, "events: create")
}

func TestUnionAndOutcomes(t *testing.T) {
	u := union(map[string]int{"a": 1}, map[string]int{"b": 2, "a": 3})
	assert.Len(t, u, 2)
	assert.Equal(t, "SOLD_OUT", supplyOutcome(pgErr("23514", "event_supply_check")))
	assert.Equal(t, "NO_SUPPLY_NOW", supplyOutcome(errors.New("timeout")))
}
