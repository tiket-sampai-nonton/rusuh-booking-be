package usecase_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/application/usecase"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/entity"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/inbound"
)

type fakePublication struct {
	calls []bool
	err   error
}

func (f *fakePublication) SetPublished(_ context.Context, _ string, published bool) error {
	f.calls = append(f.calls, published)
	return f.err
}

func TestPublishEvent_PublishIsAlwaysAllowed(t *testing.T) {
	repo := &fakePublication{}
	uc := usecase.NewPublishEvent(&fakeEventRepo{}, repo, fixedClock{t: time.Now()})
	require.NoError(t, uc.Publish(context.Background(), eventID))
	assert.Equal(t, []bool{true}, repo.calls)
}

func TestPublishEvent_UnpublishOnlyBeforeSalesOpen(t *testing.T) {
	now := time.Now()
	before := &fakeEventRepo{event: entity.Event{ID: eventID, SalesStartsAt: now.Add(time.Hour)}}
	repo := &fakePublication{}
	require.NoError(t, usecase.NewPublishEvent(before, repo, fixedClock{t: now}).Unpublish(context.Background(), eventID))
	assert.Equal(t, []bool{false}, repo.calls)

	opened := &fakeEventRepo{event: entity.Event{ID: eventID, SalesStartsAt: now.Add(-time.Minute)}}
	repo = &fakePublication{}
	err := usecase.NewPublishEvent(opened, repo, fixedClock{t: now}).Unpublish(context.Background(), eventID)
	assert.ErrorIs(t, err, inbound.ErrRulesLocked, "event yang sudah dijual tidak boleh disembunyikan lagi")
	assert.Empty(t, repo.calls)
}

func TestPublishEvent_InvalidIDAndUnknownEvent(t *testing.T) {
	uc := usecase.NewPublishEvent(&fakeEventRepo{err: inbound.ErrNotFound}, &fakePublication{}, fixedClock{t: time.Now()})
	assert.Error(t, uc.Publish(context.Background(), "nope"))
	assert.Error(t, uc.Unpublish(context.Background(), "nope"))
	assert.ErrorIs(t, uc.Unpublish(context.Background(), eventID), inbound.ErrNotFound)
}

func TestGetEventDetail_DraftIsHiddenFromThePublicButVisibleToAdmin(t *testing.T) {
	draft := detailWith(true, 2)
	draft.Event.Published = false
	uc := usecase.NewGetEventDetail(&fakeEventRepo{detail: draft}, fixedClock{t: time.Now()})

	_, err := uc.GetEventDetail(context.Background(), eventID)
	assert.ErrorIs(t, err, inbound.ErrNotFound, "draf: 404, bukan 403")

	out, err := uc.GetAdminEventDetail(context.Background(), eventID)
	require.NoError(t, err)
	assert.False(t, out.Published)
	assert.Len(t, out.Categories, 2)
}

func TestGetEventSupply_DraftIsNotFoundSoQueueRefusesToJoin(t *testing.T) {
	uc := usecase.NewGetEventSupply(&fakeEventRepo{detail: entity.EventDetail{Event: entity.Event{ID: eventID}}})
	_, err := uc.GetSupply(context.Background(), eventID)
	assert.ErrorIs(t, err, inbound.ErrNotFound)
}
