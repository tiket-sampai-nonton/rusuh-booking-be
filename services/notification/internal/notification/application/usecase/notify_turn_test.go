package usecase_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/clock"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/rabbitmq"

	mock_outbound "github.com/tiket-sampai-nonton/rusuh-booking-be/services/notification/_mock/notification/port/outbound"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/notification/internal/notification/application/usecase"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/notification/internal/notification/domain/dto"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/notification/internal/notification/domain/entity"
)

type turnMocks struct {
	repo *mock_outbound.MockIDeliveryRepository
	subs *mock_outbound.MockISubscriptionRepository
	push *mock_outbound.MockIPushSender
	pii  *mock_outbound.MockIPIIProtector
}

func newTurnMocks(t *testing.T) turnMocks {
	ctrl := gomock.NewController(t)
	return turnMocks{
		repo: mock_outbound.NewMockIDeliveryRepository(ctrl),
		subs: mock_outbound.NewMockISubscriptionRepository(ctrl),
		push: mock_outbound.NewMockIPushSender(ctrl),
		pii:  mock_outbound.NewMockIPIIProtector(ctrl),
	}
}

func soonPayload() dto.TurnSoonPayload {
	return dto.TurnSoonPayload{EventID: "e1", QueueEntryID: "q1", VisitorID: "v1"}
}

func sub() entity.PushSubscription {
	return entity.PushSubscription{VisitorID: "v1", EventID: "e1", Platform: entity.PlatformAndroid, PushTokenEnc: []byte("enc")}
}

func TestNotifyTurnSoon_SendsPush(t *testing.T) {
	m := newTurnMocks(t)
	uc := usecase.NewNotifyTurnSoon(m.repo, m.subs, m.push, m.pii, clock.Fixed{T: time.Now()}, testLogger())

	m.subs.EXPECT().FindByVisitorAndEvent(gomock.Any(), "v1", "e1").Return(sub(), nil)
	m.pii.EXPECT().UnsealPushToken([]byte("enc")).Return("tok", nil)
	m.pii.EXPECT().HMACRecipient("v1").Return([]byte("h"))
	m.repo.EXPECT().ClaimDelivery(gomock.Any(), gomock.Any()).Return(true, nil)
	m.push.EXPECT().Send(gomock.Any(), gomock.Any()).Return("prov", nil)
	m.repo.EXPECT().MarkSent(gomock.Any(), "m1", gomock.Any(), "prov").Return(nil)

	require.NoError(t, uc.NotifyTurnSoon(context.Background(), "m1", soonPayload()))
}

func TestNotifyTurnSoon_NoSubscriptionAcks(t *testing.T) {
	m := newTurnMocks(t)
	uc := usecase.NewNotifyTurnSoon(m.repo, m.subs, m.push, m.pii, clock.Fixed{T: time.Now()}, testLogger())

	m.subs.EXPECT().FindByVisitorAndEvent(gomock.Any(), "v1", "e1").Return(entity.PushSubscription{}, entity.ErrNotFound)

	require.NoError(t, uc.NotifyTurnSoon(context.Background(), "m1", soonPayload())) // no push
}

func TestNotifyTurnSoon_InvalidTokenDeletesAndAcks(t *testing.T) {
	m := newTurnMocks(t)
	uc := usecase.NewNotifyTurnSoon(m.repo, m.subs, m.push, m.pii, clock.Fixed{T: time.Now()}, testLogger())

	tokenErr := errors.New("UNREGISTERED")
	m.subs.EXPECT().FindByVisitorAndEvent(gomock.Any(), "v1", "e1").Return(sub(), nil)
	m.pii.EXPECT().UnsealPushToken(gomock.Any()).Return("tok", nil)
	m.pii.EXPECT().HMACRecipient(gomock.Any()).Return([]byte("h"))
	m.repo.EXPECT().ClaimDelivery(gomock.Any(), gomock.Any()).Return(true, nil)
	m.push.EXPECT().Send(gomock.Any(), gomock.Any()).Return("", tokenErr)
	m.push.EXPECT().IsTokenInvalid(tokenErr).Return(true)
	m.subs.EXPECT().Delete(gomock.Any(), "v1", "e1").Return(nil)
	m.repo.EXPECT().MarkDead(gomock.Any(), "m1", gomock.Any(), gomock.Any()).Return(nil)

	require.NoError(t, uc.NotifyTurnSoon(context.Background(), "m1", soonPayload()))
}

func TestNotifyTurnSoon_TransientPushRetries(t *testing.T) {
	m := newTurnMocks(t)
	uc := usecase.NewNotifyTurnSoon(m.repo, m.subs, m.push, m.pii, clock.Fixed{T: time.Now()}, testLogger())

	pushErr := errors.New("503")
	m.subs.EXPECT().FindByVisitorAndEvent(gomock.Any(), "v1", "e1").Return(sub(), nil)
	m.pii.EXPECT().UnsealPushToken(gomock.Any()).Return("tok", nil)
	m.pii.EXPECT().HMACRecipient(gomock.Any()).Return([]byte("h"))
	m.repo.EXPECT().ClaimDelivery(gomock.Any(), gomock.Any()).Return(true, nil)
	m.push.EXPECT().Send(gomock.Any(), gomock.Any()).Return("", pushErr)
	m.push.EXPECT().IsTokenInvalid(pushErr).Return(false)
	m.repo.EXPECT().BumpAttempt(gomock.Any(), "m1", gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)

	err := uc.NotifyTurnSoon(context.Background(), "m1", soonPayload())
	require.Error(t, err)
	assert.False(t, rabbitmq.IsPermanent(err))
}

func TestNotifyTurnArrived_DeadlinePassedDiscards(t *testing.T) {
	m := newTurnMocks(t)
	now := time.Date(2026, 10, 20, 5, 0, 0, 0, time.UTC)
	uc := usecase.NewNotifyTurnArrived(m.repo, m.subs, m.push, m.pii, clock.Fixed{T: now}, testLogger())

	m.pii.EXPECT().HMACRecipient("v1").Return([]byte("h"))
	m.repo.EXPECT().ClaimDelivery(gomock.Any(), gomock.Any()).Return(true, nil)
	m.repo.EXPECT().MarkExpired(gomock.Any(), "m1", gomock.Any()).Return(nil)

	p := dto.TurnArrivedPayload{EventID: "e1", QueueEntryID: "q1", VisitorID: "v1", ClaimDeadline: "2026-10-20T04:03:00Z"}
	require.NoError(t, uc.NotifyTurnArrived(context.Background(), "m1", p)) // no push
}

func TestNotifyTurnArrived_SendsBeforeDeadline(t *testing.T) {
	m := newTurnMocks(t)
	now := time.Date(2026, 10, 20, 4, 0, 0, 0, time.UTC)
	uc := usecase.NewNotifyTurnArrived(m.repo, m.subs, m.push, m.pii, clock.Fixed{T: now}, testLogger())

	m.subs.EXPECT().FindByVisitorAndEvent(gomock.Any(), "v1", "e1").Return(sub(), nil)
	m.pii.EXPECT().UnsealPushToken(gomock.Any()).Return("tok", nil)
	m.pii.EXPECT().HMACRecipient("v1").Return([]byte("h"))
	m.repo.EXPECT().ClaimDelivery(gomock.Any(), gomock.Any()).Return(true, nil)
	m.push.EXPECT().Send(gomock.Any(), gomock.Any()).Return("prov", nil)
	m.repo.EXPECT().MarkSent(gomock.Any(), "m1", gomock.Any(), "prov").Return(nil)

	p := dto.TurnArrivedPayload{EventID: "e1", QueueEntryID: "q1", VisitorID: "v1", ClaimDeadline: "2026-10-20T04:03:00Z"}
	require.NoError(t, uc.NotifyTurnArrived(context.Background(), "m1", p))
}

func TestNotifyTurnArrived_BadDeadlinePermanent(t *testing.T) {
	m := newTurnMocks(t)
	uc := usecase.NewNotifyTurnArrived(m.repo, m.subs, m.push, m.pii, clock.Fixed{T: time.Now()}, testLogger())

	p := dto.TurnArrivedPayload{EventID: "e1", QueueEntryID: "q1", VisitorID: "v1", ClaimDeadline: "not-a-time"}
	err := uc.NotifyTurnArrived(context.Background(), "m1", p)
	require.Error(t, err)
	assert.True(t, rabbitmq.IsPermanent(err))
}
