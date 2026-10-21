package mapper_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

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
			{Category: entity.Category{ID: "c1", Code: "VIP", PriceMinor: 1500000}, Available: 5},
		},
	}
	sess := mapper.ToBookingSession(admission(vo.AdmissionActive), detail, nil, time.Now())
	assert.Len(t, sess.Categories, 1)
	assert.Equal(t, "VIP", sess.Categories[0].Code)
	assert.Equal(t, 5, sess.Categories[0].Available)
}
