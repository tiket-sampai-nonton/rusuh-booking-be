//go:build e2e

package e2e

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// 1x1 PNG.
const tinyPNG = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNkYPhfDwAChwGA60e6kgAAAABJRU5ErkJggg=="

func upload(t *testing.T, eventID, kind string, headers map[string]string, content []byte) resp {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	part, err := mw.CreateFormFile("file", "x.bin")
	require.NoError(t, err)
	_, _ = part.Write(content)
	require.NoError(t, mw.Close())
	url := bookingURL + "/internal/v1/admin/events/" + eventID + "/images?kind=" + kind
	req, err := http.NewRequest("POST", url, &buf)
	require.NoError(t, err)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	res, err := httpc.Do(req)
	require.NoError(t, err)
	defer func() { _ = res.Body.Close() }()
	raw, _ := io.ReadAll(res.Body)
	record("POST", url, headers, nil, res, raw)
	out := resp{Status: res.StatusCode, Raw: string(raw)}
	var env struct {
		Data map[string]any `json:"data"`
	}
	_ = json.Unmarshal(raw, &env)
	out.Data = env.Data
	return out
}

func TestAdminImageUpload(t *testing.T) {
	admin := adminToken(t)
	eventID, _ := newEvent(t, admin, 10)
	ah := map[string]string{"Authorization": "Bearer " + admin}
	png, err := base64.StdEncoding.DecodeString(tinyPNG)
	require.NoError(t, err)

	r := upload(t, eventID, "banner", ah, png)
	mustStatus(t, 201, r, "unggah PNG")
	url := r.str("url")
	require.Contains(t, url, "/events/"+eventID+"/banner-")

	got, err := httpc.Get(url)
	require.NoError(t, err)
	defer func() { _ = got.Body.Close() }()
	body, _ := io.ReadAll(got.Body)
	assert.Equal(t, 200, got.StatusCode)
	assert.Equal(t, png, body, "file yang disajikan sama dengan yang diunggah")
	assert.Equal(t, "image/png", got.Header.Get("Content-Type"))

	mustStatus(t, 400, upload(t, eventID, "banner", ah, []byte("<svg onload=alert(1)></svg>")), "bukan gambar")
	mustStatus(t, 400, upload(t, eventID, "poster", ah, png), "kind salah")
	mustStatus(t, 404, upload(t, "00000000-0000-0000-0000-000000000000", "image", ah, png), "event tidak ada")
	mustStatus(t, 401, upload(t, eventID, "image", nil, png), "tanpa token")
}
