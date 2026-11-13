package usecase

import (
	"context"
	"log/slog"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/clock"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/domain/dto"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/domain/entity"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/domain/mapper"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/port/inbound"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/port/outbound"
	shared "github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/shared/entity"
)

// SetQueueSettings implements inbound.ISetQueueSettingsUsecase (QSA-2/3/4).
//
// Order: static validation -> dry-run of the merged rules against the stored row -> one Booking.Supply (event exists,
// sale window) -> Apply, which re-runs the same entity.Decide inside the transaction under a per-event lock. The dry
// run only keeps invalid bodies away from Booking; the in-transaction run is authoritative. After a successful Apply
// the cached row is dropped (WithCache) so readers do not serve the old settings for up to the cache TTL.
type SetQueueSettings struct {
	settings outbound.IEventSettingsRepo
	booking  outbound.IBookingClient
	admin    outbound.IEventSettingsAdminRepo
	clock    clock.IClock
	cache    outbound.ISettingsCacheInvalidator // nil = no cache to drop
}

// NewSetQueueSettings accepts interfaces and returns the concrete usecase.
func NewSetQueueSettings(
	settings outbound.IEventSettingsRepo,
	booking outbound.IBookingClient,
	admin outbound.IEventSettingsAdminRepo,
	clk clock.IClock,
) *SetQueueSettings {
	return &SetQueueSettings{settings: settings, booking: booking, admin: admin, clock: clk}
}

// WithCache sets the settings cache dropped after each successful Apply.
func (u *SetQueueSettings) WithCache(inv outbound.ISettingsCacheInvalidator) *SetQueueSettings {
	u.cache = inv
	return u
}

// Set creates or updates the settings. The bool is true when this call created the row.
func (u *SetQueueSettings) Set(ctx context.Context, in inbound.SetSettingsInput) (dto.QueueSettings, bool, error) {
	patch := mapper.ToPatch(in.Patch)
	if errs := validateRequest(patch); len(errs) > 0 {
		return dto.QueueSettings{}, false, errs
	}

	cur, exists, err := u.settings.Get(ctx, in.EventID)
	if err != nil {
		return dto.QueueSettings{}, false, err
	}
	if exists {
		// Lock is not evaluated here (it needs Supply); only merged bounds and cross-rules.
		if _, err := entity.Decide(patch, false)(cur, true); err != nil {
			return dto.QueueSettings{}, false, err
		}
	} else if _, err := entity.Decide(patch, false)(entity.EventSettings{EventID: in.EventID}, false); err != nil {
		return dto.QueueSettings{}, false, err
	}

	snap, err := u.booking.Supply(ctx, in.EventID)
	if err != nil {
		return dto.QueueSettings{}, false, err
	}
	now := u.clock.Now()
	saleOpen := !now.Before(snap.SalesStartsAt) // server clock and Booking window only (invariant #4)

	res, err := u.admin.Apply(ctx, outbound.ApplySettingsParams{
		EventID: in.EventID,
		Actor:   in.Actor,
		Now:     now,
		Decide:  entity.Decide(patch, saleOpen),
	})
	if err != nil {
		return dto.QueueSettings{}, false, err
	}
	if u.cache != nil {
		if ierr := u.cache.Invalidate(ctx, in.EventID); ierr != nil {
			slog.WarnContext(ctx, "queue settings: gagal menghapus cache; TTL 30 detik membatasi staleness",
				"eventId", in.EventID, "error", ierr)
		}
	}
	return mapper.ToQueueSettings(res.Settings), res.Created, nil
}

// validateRequest checks the required field and the per-field bounds of what was supplied.
func validateRequest(p entity.SettingsPatch) shared.ValidationErrors {
	if p.ActiveSlotCapacity == nil {
		return shared.ValidationErrors{{Field: "activeSlotCapacity", Message: "wajib diisi"}}
	}
	return p.ValidateSupplied()
}
