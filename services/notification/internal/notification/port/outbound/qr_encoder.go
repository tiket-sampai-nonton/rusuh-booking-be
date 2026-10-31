package outbound

//go:generate go tool mockgen -source=qr_encoder.go -destination=../../../../_mock/notification/port/outbound/qr_encoder_mock.go -package=mock_outbound

// IQREncoder renders a payload as a QR code PNG.
type IQREncoder interface {
	PNG(payload string) ([]byte, error)
}
