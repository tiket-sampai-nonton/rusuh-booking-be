// Package fcm implements IPushSender against the FCM HTTP v1 API. Access tokens are minted from a
// Google service-account key via the OAuth2 JWT-bearer grant (self-contained, no SDK dependency).
package fcm

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/notification/internal/notification/port/outbound"
)

const (
	fcmScope    = "https://www.googleapis.com/auth/firebase.messaging"
	tokenURL    = "https://oauth2.googleapis.com/token"
	fcmBaseURL  = "https://fcm.googleapis.com"
	sendPathTpl = "/v1/projects/%s/messages:send"
)

// ErrTokenInvalid marks an FCM response that means the device token is permanently invalid.
var ErrTokenInvalid = errors.New("fcm: token tidak valid")

type serviceAccount struct {
	ClientEmail string `json:"client_email"`
	PrivateKey  string `json:"private_key"`
	TokenURI    string `json:"token_uri"`
	ProjectID   string `json:"project_id"`
}

// Sender implements outbound.IPushSender.
type Sender struct {
	projectID string
	baseURL   string
	sa        serviceAccount
	key       *rsa.PrivateKey
	http      *http.Client
	now       func() time.Time

	mu    sync.Mutex
	token string
	exp   time.Time
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
	key, err := parseRSAKey(sa.PrivateKey)
	if err != nil {
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
	return &Sender{projectID: projectID, baseURL: fcmBaseURL, sa: sa, key: key, http: httpClient, now: func() time.Time { return time.Now() }}, nil
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
	tok, err := s.accessToken(ctx)
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

func (s *Sender) accessToken(ctx context.Context) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.token != "" && s.now().Before(s.exp.Add(-30*time.Second)) {
		return s.token, nil
	}
	assertion, err := s.signedJWT()
	if err != nil {
		return "", err
	}
	form := url.Values{}
	form.Set("grant_type", "urn:ietf:params:oauth:grant-type:jwt-bearer")
	form.Set("assertion", assertion)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.sa.TokenURI, strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("fcm: token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := s.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("fcm: token exchange: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("fcm: token http %d: %s", resp.StatusCode, string(body))
	}
	var tr struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &tr); err != nil {
		return "", fmt.Errorf("fcm: token decode: %w", err)
	}
	s.token = tr.AccessToken
	s.exp = s.now().Add(time.Duration(tr.ExpiresIn) * time.Second)
	return s.token, nil
}

func (s *Sender) signedJWT() (string, error) {
	now := s.now()
	header := base64url(`{"alg":"RS256","typ":"JWT"}`)
	claims, err := json.Marshal(map[string]any{
		"iss":   s.sa.ClientEmail,
		"scope": fcmScope,
		"aud":   s.sa.TokenURI,
		"iat":   now.Unix(),
		"exp":   now.Add(time.Hour).Unix(),
	})
	if err != nil {
		return "", fmt.Errorf("fcm: claims: %w", err)
	}
	signingInput := header + "." + base64urlBytes(claims)
	sum := sha256.Sum256([]byte(signingInput))
	sig, err := rsa.SignPKCS1v15(rand.Reader, s.key, crypto.SHA256, sum[:])
	if err != nil {
		return "", fmt.Errorf("fcm: sign jwt: %w", err)
	}
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(sig), nil
}

func base64url(s string) string      { return base64.RawURLEncoding.EncodeToString([]byte(s)) }
func base64urlBytes(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

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
