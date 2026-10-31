// Package qrcode renders ticket QR codes (outbound.IQREncoder).
package qrcode

import (
	"fmt"

	"rsc.io/qr"
)

// scale is the pixel size of one QR module; 6 gives a ~170 px code that stays sharp when shown at 132 px.
const scale = 6

// Encoder implements outbound.IQREncoder.
type Encoder struct{}

// New returns the encoder.
func New() Encoder { return Encoder{} }

// PNG encodes payload with medium error correction.
func (Encoder) PNG(payload string) ([]byte, error) {
	c, err := qr.Encode(payload, qr.M)
	if err != nil {
		return nil, fmt.Errorf("qrcode: encode: %w", err)
	}
	c.Scale = scale
	return c.PNG(), nil
}
