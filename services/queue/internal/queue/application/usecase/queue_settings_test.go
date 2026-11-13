package usecase

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/clock"

	mock_outbound "github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/_mock/queue/port/outbound"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/domain/dto"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/domain/entity"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/port/inbound"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/port/outbound"
	shared "github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/shared/entity"
)

const setEv = "11111111-1111-1111-1111-111111111111"

func ptrInt(v int) *int    { return &v }
func ptrBool(v bool) *bool { return &v }

func TestGetQueueSettings(t *testing.T) {
	ctx := context.Background()
	t.Run("found maps to dto", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		repo := mock_outbound.NewMockIEventSettingsRepo(ctrl)
		s := entity.DefaultSettings(setEv)
		s.UpdatedAt = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
		repo.EXPECT().Get(ctx, setEv).Return(s, true, nil)
		got, err := NewGetQueueSettings(repo).Get(ctx, setEv)
		require.NoError(t, err)
		assert.Equal(t, setEv, got.EventID)
		assert.Equal(t, 500, got.ActiveSlotCapacity)
		assert.Equal(t, "2026-01-02T03:04:05Z", got.UpdatedAt)
	})
	t.Run("missing row", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		repo := mock_outbound.NewMockIEventSettingsRepo(ctrl)
		repo.EXPECT().Get(ctx, setEv).Return(entity.EventSettings{}, false, nil)
		_, err := NewGetQueueSettings(repo).Get(ctx, setEv)
		assert.ErrorIs(t, err, entity.ErrSettingsNotFound)
	})
	t.Run("repo error", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		repo := mock_outbound.NewMockIEventSettingsRepo(ctrl)
		boom := errors.New("boom")
		repo.EXPECT().Get(ctx, setEv).Return(entity.EventSettings{}, false, boom)
		_, err := NewGetQueueSettings(repo).Get(ctx, setEv)
		assert.ErrorIs(t, err, boom)
	})
}

type setFixture struct {
	repo   *mock_outbound.MockIEventSettingsRepo
	book   *mock_outbound.MockIBookingClient
	admin  *mock_outbound.MockIEventSettingsAdminRepo
	uc     *SetQueueSettings
	nowVal time.Time
}

func newSetFixture(t *testing.T, now time.Time) *setFixture {
	t.Helper()
	ctrl := gomock.NewController(t)
	f := &setFixture{
		repo:   mock_outbound.NewMockIEventSettingsRepo(ctrl),
		book:   mock_outbound.NewMockIBookingClient(ctrl),
		admin:  mock_outbound.NewMockIEventSettingsAdminRepo(ctrl),
		nowVal: now,
	}
	f.uc = NewSetQueueSettings(f.repo, f.book, f.admin, clock.Fixed{T: now})
	return f
}

func TestSetQueueSettings_InvalidInputNeverCallsBookingOrApply(t *testing.T) {
	now := time.Date(2026, 11, 6, 10, 0, 0, 0, time.UTC)
	cases := []struct {
		name  string
		patch dto.QueueSettingsRequest
		cur   *entity.EventSettings // nil = no row; stored row used for the dry-run read
		field string
	}{
		{"missing capacity", dto.QueueSettingsRequest{}, nil, "activeSlotCapacity"},
		{"capacity zero", dto.QueueSettingsRequest{ActiveSlotCapacity: ptrInt(0)}, nil, "activeSlotCapacity"},
		{"poll near above max", dto.QueueSettingsRequest{ActiveSlotCapacity: ptrInt(1), PollNearMs: ptrInt(5001)}, nil, "pollNearMs"},
		{"merged cross-rule vs stored mid", dto.QueueSettingsRequest{ActiveSlotCapacity: ptrInt(1), PollNearMs: ptrInt(3000)},
			func() *entity.EventSettings { s := entity.DefaultSettings(setEv); s.PollMidMs = 2000; return &s }(), "pollNearMs"},
		{"visitor false rejected", dto.QueueSettingsRequest{ActiveSlotCapacity: ptrInt(1), OneActivePerVisitor: ptrBool(false)},
			func() *entity.EventSettings { s := entity.DefaultSettings(setEv); return &s }(), "oneActivePerVisitor"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newSetFixture(t, now)
			if c.cur != nil {
				f.repo.EXPECT().Get(gomock.Any(), setEv).Return(*c.cur, true, nil)
			}
			// static failures are caught before any read (an unexpected Get fails the mock); only the merged
			// cross-rule case reads the stored row
			f.book.EXPECT().Supply(gomock.Any(), gomock.Any()).Times(0)
			f.admin.EXPECT().Apply(gomock.Any(), gomock.Any()).Times(0)
			_, _, err := f.uc.Set(context.Background(), inbound.SetSettingsInput{EventID: setEv, Actor: "a", Patch: c.patch})
			var ve shared.ValidationErrors
			require.ErrorAs(t, err, &ve)
			assert.Equal(t, c.field, ve[0].Field)
		})
	}
}

func TestSetQueueSettings_SupplyErrors(t *testing.T) {
	now := time.Date(2026, 11, 6, 10, 0, 0, 0, time.UTC)
	for _, serr := range []error{entity.ErrEventNotFound, entity.ErrDependencyUnavailable} {
		t.Run(serr.Error(), func(t *testing.T) {
			f := newSetFixture(t, now)
			f.repo.EXPECT().Get(gomock.Any(), setEv).Return(entity.EventSettings{}, false, nil)
			f.book.EXPECT().Supply(gomock.Any(), setEv).Return(outbound.SupplySnapshot{}, serr).Times(1)
			f.admin.EXPECT().Apply(gomock.Any(), gomock.Any()).Times(0)
			_, _, err := f.uc.Set(context.Background(), inbound.SetSettingsInput{
				EventID: setEv, Actor: "a", Patch: dto.QueueSettingsRequest{ActiveSlotCapacity: ptrInt(5)}})
			assert.ErrorIs(t, err, serr)
		})
	}
}

func TestSetQueueSettings_RepoGetError(t *testing.T) {
	f := newSetFixture(t, time.Now())
	boom := errors.New("boom")
	f.repo.EXPECT().Get(gomock.Any(), setEv).Return(entity.EventSettings{}, false, boom)
	f.book.EXPECT().Supply(gomock.Any(), gomock.Any()).Times(0)
	f.admin.EXPECT().Apply(gomock.Any(), gomock.Any()).Times(0)
	_, _, err := f.uc.Set(context.Background(), inbound.SetSettingsInput{
		EventID: setEv, Actor: "a", Patch: dto.QueueSettingsRequest{ActiveSlotCapacity: ptrInt(5)}})
	assert.ErrorIs(t, err, boom)
}

func TestSetQueueSettings_SaleOpenBoundaryAndActor(t *testing.T) {
	start := time.Date(2026, 11, 6, 10, 0, 0, 0, time.UTC)
	end := start.Add(2 * time.Hour)
	lockedPatch := dto.QueueSettingsRequest{ActiveSlotCapacity: ptrInt(5), OneActivePerEmail: ptrBool(false)}
	cases := []struct {
		name       string
		now        time.Time
		wantLocked bool
	}{
		{"1ns before start", start.Add(-time.Nanosecond), false},
		{"at start", start, true},
		{"during sale", start.Add(time.Minute), true},
		{"after end stays open", end.Add(24 * time.Hour), true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newSetFixture(t, c.now)
			cur := entity.DefaultSettings(setEv)
			f.repo.EXPECT().Get(gomock.Any(), setEv).Return(cur, true, nil)
			f.book.EXPECT().Supply(gomock.Any(), setEv).Return(outbound.SupplySnapshot{EventID: setEv, SalesStartsAt: start, SalesEndsAt: end}, nil).Times(1)
			var got outbound.ApplySettingsParams
			f.admin.EXPECT().Apply(gomock.Any(), gomock.Any()).DoAndReturn(
				func(_ context.Context, p outbound.ApplySettingsParams) (outbound.ApplySettingsResult, error) {
					got = p
					merged, err := p.Decide(cur, true)
					if err != nil {
						return outbound.ApplySettingsResult{}, err
					}
					return outbound.ApplySettingsResult{Settings: merged, Changed: true}, nil
				}).Times(1)
			_, created, err := f.uc.Set(context.Background(), inbound.SetSettingsInput{EventID: setEv, Actor: "acct-9", Patch: lockedPatch})
			assert.False(t, created)
			assert.Equal(t, "acct-9", got.Actor)
			assert.Equal(t, setEv, got.EventID)
			assert.True(t, got.Now.Equal(c.now))
			if c.wantLocked {
				assert.ErrorIs(t, err, entity.ErrRulesLocked)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestSetQueueSettings_CreatedFlagAndResponse(t *testing.T) {
	now := time.Date(2026, 11, 6, 10, 0, 0, 0, time.UTC)
	f := newSetFixture(t, now)
	f.repo.EXPECT().Get(gomock.Any(), setEv).Return(entity.EventSettings{}, false, nil)
	f.book.EXPECT().Supply(gomock.Any(), setEv).Return(outbound.SupplySnapshot{SalesStartsAt: now.Add(time.Hour)}, nil)
	stored := entity.DefaultSettings(setEv)
	stored.ActiveSlotCapacity = 42
	stored.UpdatedAt = now
	f.admin.EXPECT().Apply(gomock.Any(), gomock.Any()).Return(outbound.ApplySettingsResult{Settings: stored, Created: true, Changed: true}, nil)
	got, created, err := f.uc.Set(context.Background(), inbound.SetSettingsInput{
		EventID: setEv, Actor: "a", Patch: dto.QueueSettingsRequest{ActiveSlotCapacity: ptrInt(42)}})
	require.NoError(t, err)
	assert.True(t, created)
	assert.Equal(t, 42, got.ActiveSlotCapacity)
	assert.Equal(t, "2026-11-06T10:00:00Z", got.UpdatedAt)
}

func TestSetQueueSettings_ApplyErrorPropagates(t *testing.T) {
	f := newSetFixture(t, time.Now())
	boom := errors.New("audit failed")
	f.repo.EXPECT().Get(gomock.Any(), setEv).Return(entity.EventSettings{}, false, nil)
	f.book.EXPECT().Supply(gomock.Any(), setEv).Return(outbound.SupplySnapshot{}, nil)
	f.admin.EXPECT().Apply(gomock.Any(), gomock.Any()).Return(outbound.ApplySettingsResult{}, boom)
	_, _, err := f.uc.Set(context.Background(), inbound.SetSettingsInput{
		EventID: setEv, Actor: "a", Patch: dto.QueueSettingsRequest{ActiveSlotCapacity: ptrInt(5)}})
	assert.ErrorIs(t, err, boom)
}
