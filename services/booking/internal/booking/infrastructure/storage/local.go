// Package storage holds the image stores: a git-ignored local directory (development) and S3 (AWS).
package storage

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// Local keeps images under dir and serves them at baseURL (see Handler).
type Local struct {
	dir, baseURL string
}

// NewLocal returns a store writing under dir; baseURL is the public prefix that Handler is mounted on.
func NewLocal(dir, baseURL string) *Local {
	return &Local{dir: dir, baseURL: strings.TrimRight(baseURL, "/")}
}

// Put writes the file and returns its URL. Keys that would leave the directory are refused.
func (l *Local) Put(_ context.Context, key, _ string, body []byte) (string, error) {
	path := filepath.Join(l.dir, filepath.FromSlash(key))
	root := filepath.Clean(l.dir) + string(filepath.Separator)
	if !strings.HasPrefix(path, root) {
		return "", fmt.Errorf("storage: key %q di luar direktori", key)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return "", fmt.Errorf("storage: mkdir: %w", err)
	}
	if err := os.WriteFile(path, body, 0o640); err != nil {
		return "", fmt.Errorf("storage: write: %w", err)
	}
	return l.baseURL + "/" + key, nil
}

// Handler serves stored images; mount it at "GET /media/" (the path of baseURL).
func (l *Local) Handler() http.Handler {
	fs := http.StripPrefix("/media/", http.FileServer(http.Dir(l.dir)))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		fs.ServeHTTP(w, r)
	})
}
