package usecase_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/application/usecase"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/inbound"
)

func TestExtendDeadline_ExtendsOnce(t *testing.T) {
	uow := &fakeUoW{}
	uc := usecase.NewExtendDeadline(uow)
	err := uc.Extend(context.Background(), "bk-1", "gateway_degraded", 300)
	require.NoError(t, err)
	assert.Equal(t, 1, uow.extendCalls)
}

func TestExtendDeadline_InvalidSeconds_Zero(t *testing.T) {
	uc := usecase.NewExtendDeadline(&fakeUoW{})
	err := uc.Extend(context.Background(), "bk-1", "reason", 0)
	assert.Error(t, err)
}

func TestExtendDeadline_InvalidSeconds_TooLarge(t *testing.T) {
	uc := usecase.NewExtendDeadline(&fakeUoW{})
	err := uc.Extend(context.Background(), "bk-1", "reason", 601)
	assert.Error(t, err)
}

func TestExtendDeadline_AlreadyExtended(t *testing.T) {
	uow := &fakeUoW{extendErr: inbound.ErrDeadlineAlreadyExtended}
	uc := usecase.NewExtendDeadline(uow)
	err := uc.Extend(context.Background(), "bk-1", "reason", 300)
	assert.ErrorIs(t, err, inbound.ErrDeadlineAlreadyExtended)
}
