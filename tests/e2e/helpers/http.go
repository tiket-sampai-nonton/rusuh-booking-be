//go:build e2e

package helpers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

var httpClient = &http.Client{Timeout: 10 * time.Second}

// PostJSON sends a POST with JSON body and returns the response.
func PostJSON(ctx context.Context, url string, body any, headers map[string]string) (*http.Response, error) {
	var r io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		r = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, r)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	return httpClient.Do(req)
}

// PutJSON sends a PUT with JSON body.
func PutJSON(ctx context.Context, url string, body any, headers map[string]string) (*http.Response, error) {
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, url, bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	return httpClient.Do(req)
}

// GetJSON sends a GET request.
func GetJSON(ctx context.Context, url string, headers map[string]string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	return httpClient.Do(req)
}

// DecodeBody reads and decodes a JSON response body; closes resp.Body.
func DecodeBody(resp *http.Response, dst any) error {
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, dst)
}

// BodyString reads the response body as a string for error messages.
func BodyString(resp *http.Response) string {
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return string(b)
}

// Booking builds a full URL for the Booking service.
func Booking(env *Env, path string) string {
	return fmt.Sprintf("%s%s", env.BookingAPI, path)
}

// Queue builds a full URL for the Queue service.
func Queue(env *Env, path string) string {
	return fmt.Sprintf("%s%s", env.QueueAPI, path)
}

// User builds a full URL for the User service.
func User(env *Env, path string) string {
	return fmt.Sprintf("%s%s", env.UserAPI, path)
}

// ErrorCode extracts the error code from the standard {"meta":{"message":"CODE: ..."}} envelope.
func ErrorCode(body map[string]any) string {
	meta, _ := body["meta"].(map[string]any)
	msg, _ := meta["message"].(string)
	for i, c := range msg {
		if c == ':' {
			return strings.TrimSpace(msg[:i])
		}
	}
	return msg
}

// DataString extracts body["data"][key] as string.
func DataString(body map[string]any, key string) string {
	data, _ := body["data"].(map[string]any)
	v, _ := data[key].(string)
	return v
}
