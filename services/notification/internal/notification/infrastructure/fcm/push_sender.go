// Package fcm implements IPushSender against the FCM HTTP v1 API. Access tokens are minted from a
// Google service-account key via the OAuth2 JWT-bearer grant (tanda tangan JWT lewat golang.org/x/oauth2/jws, tanpa SDK Firebase).
package fcm

import (
	"bytes"
	"context"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/jwt"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/notification/internal/notification/port/outbound"
)

const (
	fcmScope    = "https://www.googleapis.com/auth/firebase.messaging"
	tokenURL    = "https://oauth2.googleapis.com/token" // #nosec G101 -- endpoint publik Google, bukan kredensial
	fcmBaseURL  = "https://fcm.googleapis.com"
	sendPathTpl = "/v1/projects/%s/messages:send"
)

// ErrTokenInvalid marks an FCM response that means the device token is permanently invalid.
var ErrTokenInvalid = errors.New("fcm: token tidak valid")

type serviceAccount struct {
	ClientEmail  string `json:"client_email"`
	PrivateKey   string `json:"private_key"`
	PrivateKeyID string `json:"private_key_id"`
	TokenURI     string `json:"token_uri"`
	ProjectID    string `json:"project_id"`
}

// Sender implements outbound.IPushSender.
type Sender struct {
	projectID string
	baseURL   string
	http      *http.Client
	tokens    oauth2.TokenSource // access token Google; di-cache dan diperbarui otomatis oleh oauth2
}

// NewFromCredentialsFile loads the service account JSON and returns the sender.
func NewFromCredentialsFile(projectID, credentialsPath string, httpClient *http.Client) (*Sender, error) {
	raw, err := os.ReadFile(credentialsPath)
	if err != nil {
		return nil, fmt.Errorf("fcm: baca credentials: %w", err)
	}
	var sa serviceAccount
	if err := json.Unmarshal(raw, &sa); err != nil {
		return nil, fmt.Errorf("fcm: parse credentials: %w", err)
	}
	if _, err := parseRSAKey(sa.PrivateKey); err != nil { // gagal cepat bila PEM rusak
		return nil, err
	}
	if projectID == "" {
		projectID = sa.ProjectID
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 10 * time.Second}
	}
	if sa.TokenURI == "" {
		sa.TokenURI = tokenURL
	}
	// Penukaran JWT (RS256) ke access token dikerjakan pustaka resmi golang.org/x/oauth2; tidak ada kode tanda tangan di sini.
	cfg := &jwt.Config{Email: sa.ClientEmail, PrivateKey: []byte(sa.PrivateKey), PrivateKeyID: sa.PrivateKeyID,
		Scopes: []string{fcmScope}, TokenURL: sa.TokenURI}
	ctx := context.WithValue(context.Background(), oauth2.HTTPClient, httpClient)
	return &Sender{projectID: projectID, baseURL: fcmBaseURL, http: httpClient, tokens: oauth2.ReuseTokenSource(nil, cfg.TokenSource(ctx))}, nil
}

// WithBaseURL overrides the FCM send base URL (used in tests). Returns the sender for chaining.
func (s *Sender) WithBaseURL(u string) *Sender {
	s.baseURL = u
	return s
}

// IsTokenInvalid reports whether err came from an invalid device token (delete the subscription).
func (s *Sender) IsTokenInvalid(err error) bool { return errors.Is(err, ErrTokenInvalid) }

type fcmNotification struct {
	Title string `json:"title"`
	Body  string `json:"body"`
}

type fcmMessage struct {
	Token        string            `json:"token"`
	Notification fcmNotification   `json:"notification"`
	Data         map[string]string `json:"data,omitempty"`
}

type sendRequest struct {
	Message fcmMessage `json:"message"`
}

type sendResponse struct {
	Name string `json:"name"`
}

// Send posts one message to the FCM v1 endpoint.
func (s *Sender) Send(ctx context.Context, msg outbound.PushMessage) (string, error) {
	tok, err := s.accessToken()
	if err != nil {
		return "", err
	}
	payload, err := json.Marshal(sendRequest{Message: fcmMessage{
		Token:        msg.Token,
		Notification: fcmNotification{Title: msg.Title, Body: msg.Body},
		Data:         msg.Data,
	}})
	if err != nil {
		return "", fmt.Errorf("fcm: marshal: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.baseURL+fmt.Sprintf(sendPathTpl, s.projectID), bytes.NewReader(payload))
	if err != nil {
		return "", fmt.Errorf("fcm: request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("fcm: send: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	respBody, _ := io.ReadAll(resp.Body)

	if resp.StatusCode == http.StatusOK {
		var sr sendResponse
		_ = json.Unmarshal(respBody, &sr)
		return sr.Name, nil
	}
	if isInvalidTokenResponse(resp.StatusCode, respBody) {
		return "", fmt.Errorf("%w: %s", ErrTokenInvalid, string(respBody))
	}
	return "", fmt.Errorf("fcm: http %d: %s", resp.StatusCode, string(respBody))
}

// isInvalidTokenResponse detects UNREGISTERED (404) and INVALID_ARGUMENT (400) token errors.
func isInvalidTokenResponse(status int, body []byte) bool {
	if status != http.StatusNotFound && status != http.StatusBadRequest {
		return false
	}
	b := string(body)
	return strings.Contains(b, "UNREGISTERED") || strings.Contains(b, "INVALID_ARGUMENT")
}

func (s *Sender) accessToken() (string, error) {
	t, err := s.tokens.Token()
	if err != nil {
		return "", fmt.Errorf("fcm: token: %w", err)
	}
	return t.AccessToken, nil
}

func parseRSAKey(pemKey string) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(pemKey))
	if block == nil {
		return nil, errors.New("fcm: private_key PEM tidak ditemukan")
	}
	if k, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return k, nil
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("fcm: parse private_key: %w", err)
	}
	k, ok := parsed.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("fcm: private_key bukan RSA")
	}
	return k, nil
}
