package usecase_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/apperror"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/clock"

	mock_outbound "github.com/tiket-sampai-nonton/rusuh-booking-be/services/notification/_mock/notification/port/outbound"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/notification/internal/notification/application/usecase"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/notification/internal/notification/domain/dto"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/notification/internal/notification/domain/entity"
)

func TestSubscribe_UpsertsEncryptedToken(t *testing.T) {
	ctrl := gomock.NewController(t)
	subs := mock_outbound.NewMockISubscriptionRepository(ctrl)
	pii := mock_outbound.NewMockIPIIProtector(ctrl)
	uc := usecase.NewManageSubscription(subs, pii, clock.Fixed{T: time.Now()}, time.Hour)

	pii.EXPECT().SealPushToken("dev-token").Return([]byte("enc"), nil)
	subs.EXPECT().Upsert(gomock.Any(), gomock.AssignableToTypeOf(entity.PushSubscription{})).
		DoAndReturn(func(_ context.Context, s entity.PushSubscription) error {
			assert.Equal(t, "v1", s.VisitorID)
			assert.Equal(t, entity.PlatformAndroid, s.Platform)
			assert.Equal(t, []byte("enc"), s.PushTokenEnc)
			assert.False(t, s.ExpiresAt.IsZero())
			return nil
		})

	req := dto.SubscribeRequest{EventID: "e1", Platform: "ANDROID", PushToken: "dev-token"}
	require.NoError(t, uc.Subscribe(context.Background(), "v1", req))
}

func TestSubscribe_ValidationError(t *testing.T) {
	ctrl := gomock.NewController(t)
	uc := usecase.NewManageSubscription(
		mock_outbound.NewMockISubscriptionRepository(ctrl),
		mock_outbound.NewMockIPIIProtector(ctrl),
		clock.Fixed{T: time.Now()}, time.Hour)

	req := dto.SubscribeRequest{EventID: "", Platform: "WEB", PushToken: ""}
	err := uc.Subscribe(context.Background(), "v1", req)
	require.Error(t, err)
	ae := apperror.From(err)
	assert.Equal(t, apperror.CodeValidationFailed, ae.Code)
}

func TestUnsubscribe_Deletes(t *testing.T) {
	ctrl := gomock.NewController(t)
	subs := mock_outbound.NewMockISubscriptionRepository(ctrl)
	uc := usecase.NewManageSubscription(subs, mock_outbound.NewMockIPIIProtector(ctrl), clock.Fixed{T: time.Now()}, time.Hour)

	subs.EXPECT().Delete(gomock.Any(), "v1", "e1").Return(nil)
	require.NoError(t, uc.Unsubscribe(context.Background(), "v1", "e1"))
}
