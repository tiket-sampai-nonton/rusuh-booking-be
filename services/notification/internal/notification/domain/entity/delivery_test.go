package entity_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/notification/internal/notification/domain/entity"
)

func TestDelivery_ChannelHelpers(t *testing.T) {
	assert.True(t, entity.Delivery{Channel: entity.ChannelEmail}.IsEmail())
	assert.False(t, entity.Delivery{Channel: entity.ChannelEmail}.IsPush())
	assert.True(t, entity.Delivery{Channel: entity.ChannelPush}.IsPush())
}

func TestPlatform_Valid(t *testing.T) {
	assert.True(t, entity.PlatformAndroid.Valid())
	assert.True(t, entity.PlatformIOS.Valid())
	assert.False(t, entity.Platform("WEB").Valid())
}
