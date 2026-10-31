package usecase

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/dto"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/inbound"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/outbound"
)

// imageTypes lists the accepted content types and file extensions.
var imageTypes = map[string]string{"image/jpeg": ".jpg", "image/png": ".png", "image/webp": ".webp"}

// sniffImage returns the content type from the file's magic bytes (never from the client), or "".
func sniffImage(b []byte) string {
	switch {
	case bytes.HasPrefix(b, []byte("\xff\xd8\xff")):
		return "image/jpeg"
	case bytes.HasPrefix(b, []byte("\x89PNG\r\n\x1a\n")):
		return "image/png"
	case len(b) >= 12 && bytes.HasPrefix(b, []byte("RIFF")) && bytes.Equal(b[8:12], []byte("WEBP")):
		return "image/webp"
	}
	return ""
}

// UploadEventImage stores an event image and records its URL on the event.
type UploadEventImage struct {
	repo  outbound.IEventWriteRepo
	store outbound.IImageStore
}

// NewUploadEventImage wires the usecase.
func NewUploadEventImage(repo outbound.IEventWriteRepo, store outbound.IImageStore) *UploadEventImage {
	return &UploadEventImage{repo: repo, store: store}
}

// Upload validates kind, size, and content type, stores the file, and returns its public URL.
func (uc *UploadEventImage) Upload(ctx context.Context, eventID, kind string, data []byte) (string, error) {
	switch kind {
	case "image", "banner", "thumbnail":
	default:
		return "", inbound.Validation("kind", "harus image, banner, atau thumbnail")
	}
	if len(data) == 0 || len(data) > inbound.MaxImageBytes {
		return "", inbound.Validation("file", "ukuran file 1 byte sampai 5 MB")
	}
	contentType := sniffImage(data)
	ext, ok := imageTypes[contentType]
	if !ok {
		return "", inbound.Validation("file", "harus JPEG, PNG, atau WebP")
	}
	// A no-op config update confirms the event exists (and that eventID is a UUID), so the key below is safe
	// and no orphan file is stored for an unknown event.
	if err := uc.repo.UpdateConfig(ctx, eventID, dto.UpdateEventConfigRequest{}); err != nil {
		return "", err
	}
	suffix := make([]byte, 8)
	if _, err := rand.Read(suffix); err != nil {
		return "", fmt.Errorf("images: random name: %w", err)
	}
	url, err := uc.store.Put(ctx, fmt.Sprintf("events/%s/%s-%s%s", eventID, kind, hex.EncodeToString(suffix), ext), contentType, data)
	if err != nil {
		return "", fmt.Errorf("images: store: %w", err)
	}
	if err := uc.repo.SetImageURL(ctx, eventID, kind, url); err != nil {
		return "", err
	}
	return url, nil
}
