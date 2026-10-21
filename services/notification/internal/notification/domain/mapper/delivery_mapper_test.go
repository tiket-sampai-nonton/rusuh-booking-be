package mapper_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/notification/internal/notification/domain/dto"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/notification/internal/notification/domain/entity"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/notification/internal/notification/domain/mapper"
)

func TestNewEmailDelivery(t *testing.T) {
	d := mapper.NewEmailDelivery("msg", entity.TypeBookingPaid, "booking-1", []byte("h"))
	assert.Equal(t, "msg", d.SourceMessageID)
	assert.Equal(t, entity.ChannelEmail, d.Channel)
	assert.Equal(t, entity.StatusSending, d.Status)
	require.NotNil(t, d.BookingID)
	assert.Equal(t, "booking-1", *d.BookingID)
	assert.Nil(t, d.QueueEntryID)
}

func TestNewPushDelivery(t *testing.T) {
	d := mapper.NewPushDelivery("msg", entity.TypeTurnArrived, "q-1", []byte("h"))
	assert.Equal(t, entity.ChannelPush, d.Channel)
	require.NotNil(t, d.QueueEntryID)
	assert.Equal(t, "q-1", *d.QueueEntryID)
	assert.Nil(t, d.BookingID)
}

func TestToSubscription(t *testing.T) {
	req := dto.SubscribeRequest{EventID: "e1", Platform: "IOS", PushToken: "t"}
	s := mapper.ToSubscription("v1", req, []byte("enc"))
	assert.Equal(t, "v1", s.VisitorID)
	assert.Equal(t, "e1", s.EventID)
	assert.Equal(t, entity.PlatformIOS, s.Platform)
	assert.Equal(t, []byte("enc"), s.PushTokenEnc)
}
