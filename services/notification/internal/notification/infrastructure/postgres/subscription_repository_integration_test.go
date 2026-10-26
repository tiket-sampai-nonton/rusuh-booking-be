//go:build integration

package postgres

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/notification/internal/notification/domain/entity"
)

const (
	visitorID = "33333333-3333-3333-3333-333333333333"
	eventID   = "44444444-4444-4444-4444-444444444444"
)

func sub(token string, platform entity.Platform) entity.PushSubscription {
	now := time.Now().UTC().Truncate(time.Second)
	return entity.PushSubscription{
		VisitorID: visitorID, EventID: eventID, Platform: platform,
		PushTokenEnc: []byte(token), CreatedAt: now, ExpiresAt: now.Add(24 * time.Hour),
	}
}

func TestSubscriptions_UpsertFindDelete(t *testing.T) {
	ctx := context.Background()
	repo := NewSubscriptions(setup(t))

	_, err := repo.FindByVisitorAndEvent(ctx, visitorID, eventID)
	require.ErrorIs(t, err, entity.ErrNotFound)

	require.NoError(t, repo.Upsert(ctx, sub("enc-1", entity.PlatformAndroid)))
	require.NoError(t, repo.Upsert(ctx, sub("enc-2", entity.PlatformIOS)), "subscribe ulang mengganti token, bukan duplikat")

	got, err := repo.FindByVisitorAndEvent(ctx, visitorID, eventID)
	require.NoError(t, err)
	assert.Equal(t, []byte("enc-2"), got.PushTokenEnc)
	assert.Equal(t, entity.PlatformIOS, got.Platform)

	require.NoError(t, repo.Delete(ctx, visitorID, eventID))
	require.NoError(t, repo.Delete(ctx, visitorID, eventID), "delete idempoten")
	_, err = repo.FindByVisitorAndEvent(ctx, visitorID, eventID)
	require.ErrorIs(t, err, entity.ErrNotFound)

	_, err = repo.FindByVisitorAndEvent(ctx, "bukan-uuid", eventID)
	require.ErrorIs(t, err, entity.ErrNotFound, "id tidak valid diperlakukan sebagai tidak ditemukan")
}
