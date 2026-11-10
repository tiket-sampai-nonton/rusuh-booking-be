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

func TestUnsubscribe(t *testing.T) {
	const validEvent = "0b9a3f0e-6b1c-4c1e-9d55-2f1d7a8e4c10"
	tests := []struct {
		name      string
		eventID   string
		expectDel bool
		wantCode  apperror.Code
	}{
		{"uuid valid menghapus", validEvent, true, ""},
		{"uuid huruf besar valid", "0B9A3F0E-6B1C-4C1E-9D55-2F1D7A8E4C10", true, ""},
		{"bukan uuid ditolak sebelum DB", "not-a-uuid", false, apperror.CodeValidationFailed},
		{"kosong ditolak", "", false, apperror.CodeValidationFailed},
		{"uuid tanpa tanda hubung ditolak", "0b9a3f0e6b1c4c1e9d552f1d7a8e4c10", false, apperror.CodeValidationFailed},
		{"injeksi ditolak", validEvent + "'; DROP TABLE x;--", false, apperror.CodeValidationFailed},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			subs := mock_outbound.NewMockISubscriptionRepository(ctrl)
			uc := usecase.NewManageSubscription(subs, mock_outbound.NewMockIPIIProtector(ctrl), clock.Fixed{T: time.Now()}, time.Hour)
			if tc.expectDel {
				subs.EXPECT().Delete(gomock.Any(), "v1", tc.eventID).Return(nil)
			}
			err := uc.Unsubscribe(context.Background(), "v1", tc.eventID)
			if tc.wantCode == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Equal(t, tc.wantCode, apperror.From(err).Code)
		})
	}
}
