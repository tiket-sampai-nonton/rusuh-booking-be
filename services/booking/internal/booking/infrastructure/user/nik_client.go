// Package user provides the HTTP client adapter for User Service internal APIs.
package user

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/crypto"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/inbound"
)

// AAD used by User Service when sealing nik_enc (must match security.go in user service).
const aadUserNIK = "users.accounts.nik"

// AAD used by Booking for attendee_nik_enc.
const aadBookingNIK = "booking.attendee_nik"

// NIKClient implements outbound.IUserNIKClient via HTTP call to User Service.
// It re-seals the ciphertext: unseals with User's AAD then seals with Booking's AAD so the
// result is stored directly as attendee_nik_enc. No NIK plaintext transits the network (ADR-030).
type NIKClient struct {
	baseURL string
	sealer  *crypto.Sealer
	client  *http.Client
}

// NewNIKClient wires the client. baseURL is the User Service base URL (e.g. "http://user-svc:8080").
// sealer uses the shared PII_ENC_KEY.
func NewNIKClient(baseURL string, sealer *crypto.Sealer) *NIKClient {
	return &NIKClient{
		baseURL: strings.TrimRight(baseURL, "/"),
		sealer:  sealer,
		client:  &http.Client{},
	}
}

// GetNIKEnc fetches the buyer's NIK from User Service and returns ciphertext ready to store as
// attendee_nik_enc (re-sealed from User's AAD to Booking's AAD in memory).
func (c *NIKClient) GetNIKEnc(ctx context.Context, accountID, bearerToken string) ([]byte, error) {
	url := fmt.Sprintf("%s/internal/v1/accounts/%s/attendee-nik-enc", c.baseURL, accountID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, inbound.ErrDependency
	}
	req.Header.Set("Authorization", "Bearer "+bearerToken)
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, inbound.ErrDependency
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusNotFound {
		return nil, inbound.ErrNIKNotLinked
	}
	if resp.StatusCode != http.StatusOK {
		return nil, inbound.ErrDependency
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err != nil {
		return nil, inbound.ErrDependency
	}
	var payload struct {
		Data struct {
			NikEnc string `json:"nikEnc"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &payload); err != nil || payload.Data.NikEnc == "" {
		return nil, inbound.ErrDependency
	}

	userEnc, err := base64.StdEncoding.DecodeString(payload.Data.NikEnc)
	if err != nil {
		return nil, inbound.ErrDependency
	}

	// Unseal with User's AAD then re-seal with Booking's AAD — plaintext stays in process memory only.
	plaintext, err := c.sealer.Open(userEnc, []byte(aadUserNIK))
	if err != nil {
		return nil, inbound.ErrDependency
	}
	bookingEnc, err := c.sealer.Seal(plaintext, []byte(aadBookingNIK))
	if err != nil {
		return nil, inbound.ErrDependency
	}
	return bookingEnc, nil
}
