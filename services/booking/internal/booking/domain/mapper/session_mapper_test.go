package mapper_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/entity"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/mapper"
	vo "github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/valueobject"
)

func admission(status vo.AdmissionStatus) entity.Admission {
	return entity.Admission{
		ID:                      "sess-1",
		EventID:                 "ev-1",
		Status:                  status,
		Units:                   3,
		ExpiresAt:               time.Now().Add(time.Hour),
		CfgAllowMixedCategories: true,
		CfgEditEnabled:          true,
		CfgMaxEdits:             3,
	}
}

func TestToBookingSession_PhaseSelecting(t *testing.T) {
	sess := mapper.ToBookingSession(admission(vo.AdmissionActive), entity.EventDetail{}, nil, time.Now())
	assert.Equal(t, "SELECTING", sess.Phase)
	assert.Equal(t, "sess-1", sess.SessionID)
}

func TestToBookingSession_PhaseSelectingReserved(t *testing.T) {
	sess := mapper.ToBookingSession(admission(vo.AdmissionReserved), entity.EventDetail{}, nil, time.Now())
	assert.Equal(t, "SELECTING", sess.Phase)
}

func TestToBookingSession_PhaseEditable(t *testing.T) {
	sess := mapper.ToBookingSession(admission(vo.AdmissionEditable), entity.EventDetail{}, nil, time.Now())
	assert.Equal(t, "EDITABLE", sess.Phase)
}

func TestToBookingSession_PhaseLocked(t *testing.T) {
	sess := mapper.ToBookingSession(admission(vo.AdmissionConverted), entity.EventDetail{}, nil, time.Now())
	assert.Equal(t, "LOCKED", sess.Phase)
}

func TestToBookingSession_PhaseEnded(t *testing.T) {
	sess := mapper.ToBookingSession(admission(vo.AdmissionExpired), entity.EventDetail{}, nil, time.Now())
	assert.Equal(t, "ENDED", sess.Phase)
}

func TestToBookingSession_WithCategories(t *testing.T) {
	detail := entity.EventDetail{
		Categories: []entity.CategoryDetail{
			{Category: entity.Category{ID: "c1", Code: "VIP", PriceMinor: 1500000}, CategoryFree: 5, EventFree: 5, Available: 5},
		},
	}
	sess := mapper.ToBookingSession(admission(vo.AdmissionActive), detail, nil, time.Now())
	assert.Len(t, sess.Categories, 1)
	assert.Equal(t, "VIP", sess.Categories[0].Code)
	assert.Equal(t, 5, sess.Categories[0].Available)
}

// C5.1: the session view shows min(category free, event free + the buyer's own outstanding units). Event free already
// excludes the buyer's reservation, so the view adds it back for this buyer only.
func TestToBookingSession_AddsBuyerOwnOutstandingUnits(t *testing.T) {
	tests := []struct {
		name        string
		catFree     int
		eventFree   int
		outstanding int
		want        int
	}{
		{name: "event fully allocated shows own units", catFree: 10, eventFree: 0, outstanding: 3, want: 3},
		{name: "no own units and no event free shows zero", catFree: 10, eventFree: 0, outstanding: 0, want: 0},
		{name: "category free caps own units", catFree: 2, eventFree: 0, outstanding: 3, want: 2},
		{name: "event free plus own units", catFree: 10, eventFree: 4, outstanding: 3, want: 7},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			detail := entity.EventDetail{Categories: []entity.CategoryDetail{{
				Category:     entity.Category{ID: "c1", Code: "VIP"},
				CategoryFree: tt.catFree,
				EventFree:    tt.eventFree,
				Available:    min(tt.catFree, tt.eventFree),
			}}}
			a := admission(vo.AdmissionActive)
			a.OutstandingUnits = tt.outstanding
			sess := mapper.ToBookingSession(a, detail, nil, time.Now())
			require.Len(t, sess.Categories, 1)
			assert.Equal(t, tt.want, sess.Categories[0].Available)
		})
	}
}

func TestSessionView_ExposesOutstandingUnits(t *testing.T) {
	a := entity.Admission{ID: "s", Units: 3, OutstandingUnits: 2, Status: vo.AdmissionActive}
	v := mapper.ToBookingSession(a, entity.EventDetail{}, nil, time.Now())
	assert.Equal(t, 3, v.ReservedUnits)
	assert.Equal(t, 2, v.OutstandingUnits)
}
