package httpx

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/apperror"
)

// DefaultMaxBody is the request body limit for JSON endpoints.
const DefaultMaxBody = 16 << 10

// DecodeJSON reads one JSON object into v with a size limit and unknown fields rejected.
// Error yang dikembalikan sudah berbentuk apperror VALIDATION_ERROR (400).
func DecodeJSON(w http.ResponseWriter, r *http.Request, v any, maxBytes int64) error {
	if maxBytes <= 0 {
		maxBytes = DefaultMaxBody
	}
	if ct := r.Header.Get("Content-Type"); ct != "" && !strings.HasPrefix(ct, "application/json") {
		return apperror.New(apperror.CodeValidationFailed, http.StatusUnsupportedMediaType, "Content-Type harus application/json")
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			return apperror.New(apperror.CodeValidationFailed, http.StatusRequestEntityTooLarge, "body terlalu besar")
		}
		return apperror.Wrap(err, apperror.CodeValidationFailed, http.StatusBadRequest, fmt.Sprintf("body JSON tidak valid: %s", safeJSONError(err)))
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return apperror.New(apperror.CodeValidationFailed, http.StatusBadRequest, "body harus satu objek JSON")
	}
	return nil
}

func safeJSONError(err error) string {
	var se *json.SyntaxError
	var te *json.UnmarshalTypeError
	switch {
	case errors.As(err, &se):
		return "sintaks salah"
	case errors.As(err, &te):
		return "tipe field " + te.Field + " salah"
	case strings.HasPrefix(err.Error(), "json: unknown field"):
		return strings.TrimPrefix(err.Error(), "json: ")
	case errors.Is(err, io.EOF):
		return "body kosong"
	default:
		return "format salah"
	}
}

// ClientIP returns the client address. Di belakang ALB, alamat asli ada di elemen terakhir yang
// ditambahkan ALB pada X-Forwarded-For; nilai yang bisa dipalsukan klien ada di sebelah kirinya.
func ClientIP(r *http.Request, trustProxy bool) string {
	if trustProxy {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			parts := strings.Split(xff, ",")
			if ip := strings.TrimSpace(parts[len(parts)-1]); net.ParseIP(ip) != nil {
				return ip
			}
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// ClientIPHops returns the client address when `hops` trusted proxies each appended one entry to X-Forwarded-For
// (1 = ALB only, 2 = CloudFront then ALB). All X-Forwarded-For header lines are joined in order, then the entry
// `hops` from the right is used, so anything a client wrote to the left stays ignored. Falls back to the
// connection address when trustProxy is off, hops < 1, there are fewer entries than hops, or the entry is not an IP.
func ClientIPHops(r *http.Request, trustProxy bool, hops int) string {
	remote := remoteHost(r)
	if !trustProxy || hops < 1 {
		return remote
	}
	lines := r.Header.Values("X-Forwarded-For")
	if len(lines) == 0 {
		return remote
	}
	parts := strings.Split(strings.Join(lines, ","), ",")
	if len(parts) < hops {
		return remote
	}
	if ip := net.ParseIP(strings.TrimSpace(parts[len(parts)-hops])); ip != nil {
		return ip.String()
	}
	return remote
}

func remoteHost(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// BearerToken extracts the token from "Authorization: Bearer <token>".
func BearerToken(r *http.Request) (string, bool) {
	h := r.Header.Get("Authorization")
	scheme, tok, ok := strings.Cut(h, " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") || strings.TrimSpace(tok) == "" {
		return "", false
	}
	return strings.TrimSpace(tok), true
}
