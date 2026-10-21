package usecase_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/application/usecase"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/dto"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/inbound"
)

func TestUpdateEventConfig_Success(t *testing.T) {
	repo := &fakeEventWriteRepo{}
	uc := usecase.NewUpdateEventConfig(repo)
	err := uc.Update(context.Background(), "ev-1", dto.UpdateEventConfigRequest{})
	require.NoError(t, err)
}

func TestUpdateEventConfig_RulesLocked(t *testing.T) {
	repo := &fakeEventWriteRepo{updateErr: inbound.ErrRulesLocked}
	uc := usecase.NewUpdateEventConfig(repo)
	err := uc.Update(context.Background(), "ev-1", dto.UpdateEventConfigRequest{})
	assert.ErrorIs(t, err, inbound.ErrRulesLocked)
}

func TestUpdateEventConfig_NoOpIfEmpty(t *testing.T) {
	repo := &fakeEventWriteRepo{}
	uc := usecase.NewUpdateEventConfig(repo)
	// empty request is valid (no-op)
	err := uc.Update(context.Background(), "ev-1", dto.UpdateEventConfigRequest{})
	assert.NoError(t, err)
}
