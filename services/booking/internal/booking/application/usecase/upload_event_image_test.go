package usecase_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/application/usecase"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/inbound"
)

type fakeImageStore struct {
	key, contentType string
	err              error
}

func (f *fakeImageStore) Put(_ context.Context, key, contentType string, _ []byte) (string, error) {
	f.key, f.contentType = key, contentType
	return "https://cdn.test/" + key, f.err
}

var (
	pngBytes  = append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 32)...)
	jpegBytes = append([]byte("\xff\xd8\xff\xe0"), make([]byte, 32)...)
	webpBytes = append([]byte("RIFF\x24\x00\x00\x00WEBPVP8 "), make([]byte, 32)...)
)

func TestUploadEventImage_StoresValidImagesAndRecordsTheURL(t *testing.T) {
	for name, tc := range map[string]struct {
		data []byte
		ext  string
		ct   string
	}{"png": {pngBytes, ".png", "image/png"}, "jpeg": {jpegBytes, ".jpg", "image/jpeg"}, "webp": {webpBytes, ".webp", "image/webp"}} {
		t.Run(name, func(t *testing.T) {
			repo, store := &fakeEventWriteRepo{}, &fakeImageStore{}
			url, err := usecase.NewUploadEventImage(repo, store).Upload(context.Background(), "ev1", "banner", tc.data)
			require.NoError(t, err)
			assert.Equal(t, tc.ct, store.contentType, "tipe dari isi file")
			assert.True(t, strings.HasPrefix(store.key, "events/ev1/banner-") && strings.HasSuffix(store.key, tc.ext), store.key)
			assert.Equal(t, url, repo.imageURL)
			assert.Equal(t, "banner", repo.imageKind)
		})
	}
}

func TestUploadEventImage_RejectsBadInputBeforeStoring(t *testing.T) {
	uc := func(repo *fakeEventWriteRepo, store *fakeImageStore) *usecase.UploadEventImage {
		return usecase.NewUploadEventImage(repo, store)
	}
	ctx := context.Background()
	store := &fakeImageStore{}
	_, err := uc(&fakeEventWriteRepo{}, store).Upload(ctx, "ev1", "poster", pngBytes)
	assert.Error(t, err, "kind tidak dikenal")
	_, err = uc(&fakeEventWriteRepo{}, store).Upload(ctx, "ev1", "image", nil)
	assert.Error(t, err, "kosong")
	_, err = uc(&fakeEventWriteRepo{}, store).Upload(ctx, "ev1", "image", append(pngBytes, bytes.Repeat([]byte{0}, inbound.MaxImageBytes)...))
	assert.Error(t, err, "terlalu besar")
	_, err = uc(&fakeEventWriteRepo{}, store).Upload(ctx, "ev1", "image", []byte("<svg onload=alert(1)>"))
	assert.Error(t, err, "bukan JPEG/PNG/WebP")
	_, err = uc(&fakeEventWriteRepo{updateErr: inbound.ErrNotFound}, store).Upload(ctx, "ev1", "image", pngBytes)
	assert.ErrorIs(t, err, inbound.ErrNotFound)
	assert.Empty(t, store.key, "tidak ada file tersimpan untuk input buruk atau event tak dikenal")
}

func TestUploadEventImage_ReportsStoreAndRepoFailures(t *testing.T) {
	ctx := context.Background()
	_, err := usecase.NewUploadEventImage(&fakeEventWriteRepo{}, &fakeImageStore{err: errors.New("disk penuh")}).Upload(ctx, "ev1", "image", pngBytes)
	assert.ErrorContains(t, err, "disk penuh")
	_, err = usecase.NewUploadEventImage(&fakeEventWriteRepo{imageErr: errors.New("db down")}, &fakeImageStore{}).Upload(ctx, "ev1", "image", pngBytes)
	assert.ErrorContains(t, err, "db down")
}
