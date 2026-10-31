package qrcode

import (
	"bytes"
	"image/png"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEncoder_PNGIsAValidImage(t *testing.T) {
	raw, err := New().PNG("RBT:ABCDEFGHIJKL")
	require.NoError(t, err)
	img, err := png.Decode(bytes.NewReader(raw))
	require.NoError(t, err)
	assert.Greater(t, img.Bounds().Dx(), 100)
	assert.Equal(t, img.Bounds().Dx(), img.Bounds().Dy())
}

func TestEncoder_RejectsAPayloadTooLargeForAQRCode(t *testing.T) {
	_, err := New().PNG(string(bytes.Repeat([]byte("x"), 10000)))
	assert.Error(t, err)
}
