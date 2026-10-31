package storage

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLocal_PutServesTheFileAndRefusesTraversal(t *testing.T) {
	dir := t.TempDir()
	l := NewLocal(dir, "http://localhost:8082/media/")
	url, err := l.Put(context.Background(), "events/e1/image-a.png", "image/png", []byte("PNGDATA"))
	require.NoError(t, err)
	assert.Equal(t, "http://localhost:8082/media/events/e1/image-a.png", url)
	got, err := os.ReadFile(filepath.Join(dir, "events", "e1", "image-a.png"))
	require.NoError(t, err)
	assert.Equal(t, "PNGDATA", string(got))

	rec := httptest.NewRecorder()
	l.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/media/events/e1/image-a.png", nil))
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "nosniff", rec.Header().Get("X-Content-Type-Options"))
	assert.Equal(t, "PNGDATA", rec.Body.String())

	_, err = l.Put(context.Background(), "../../etc/evil", "image/png", []byte("x"))
	assert.Error(t, err)
	// A path that cannot be created (a file where a directory is needed) is reported.
	_, err = l.Put(context.Background(), "events/e1/image-a.png/x.png", "image/png", []byte("x"))
	assert.Error(t, err)
}

func TestS3_PutUploadsToTheBucketAndReturnsThePublicURL(t *testing.T) {
	var gotPath, gotType string
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotType = r.URL.Path, r.Header.Get("Content-Type")
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test")
	t.Setenv("AWS_REGION", "ap-southeast-3")

	s, err := NewS3(context.Background(), "bucket-x", srv.URL, "https://cdn.example/")
	require.NoError(t, err)
	url, err := s.Put(context.Background(), "events/e1/banner-a.webp", "image/webp", []byte("WEBP"))
	require.NoError(t, err)
	assert.Equal(t, "https://cdn.example/events/e1/banner-a.webp", url)
	assert.Equal(t, "/bucket-x/events/e1/banner-a.webp", gotPath)
	assert.Equal(t, "image/webp", gotType)
	assert.Contains(t, string(gotBody), "WEBP")

	srv.Close()
	_, err = s.Put(context.Background(), "k", "image/png", []byte("x"))
	assert.Error(t, err, "kegagalan S3 dilaporkan")
}
