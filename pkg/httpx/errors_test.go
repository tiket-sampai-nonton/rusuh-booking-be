package httpx

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/apperror"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/logger"
)

// captureLog mengganti slog default dengan logger paket logger (redaksi aktif) ke buffer.
func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	l, err := logger.New(&buf, logger.Config{Level: "debug", Format: "json"}, "svc", "api")
	require.NoError(t, err)
	prev := slog.Default()
	slog.SetDefault(l)
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &buf
}

func writeErr(t *testing.T, err error) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h := Chain(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { WriteError(w, r, err) }), RequestID())
	req := httptest.NewRequest(http.MethodPost, "/x", nil)
	req.Header.Set(HeaderRequestID, "req-1")
	h.ServeHTTP(rec, req)
	return rec
}

func TestWriteError_BodyOnlyCodeAndMessage(t *testing.T) {
	captureLog(t)
	err := apperror.New("BOOKING_LOCKED", http.StatusConflict, "Pesanan sudah terkunci").
		WithDetails(map[string]string{"bookingId": "bk-1"})

	rec := writeErr(t, err)

	assert.Equal(t, http.StatusConflict, rec.Code)
	assert.JSONEq(t, `{"meta":{"code":409,"message":"BOOKING_LOCKED: Pesanan sudah terkunci"},"data":null}`, rec.Body.String())
	assert.Equal(t, "req-1", rec.Header().Get(HeaderRequestID))
}

func TestWriteError_InternalHidesCause(t *testing.T) {
	captureLog(t)
	rec := writeErr(t, apperror.Internal(assert.AnError))

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.JSONEq(t, `{"meta":{"code":500,"message":"INTERNAL_ERROR: terjadi kesalahan internal"},"data":null}`, rec.Body.String())
	assert.NotContains(t, rec.Body.String(), assert.AnError.Error())
}

// Detail yang dibuang dari body harus tetap ada di log dengan requestId yang sama.
func TestWriteError_LogsDetailsAndCause(t *testing.T) {
	buf := captureLog(t)
	err := apperror.Wrap(assert.AnError, "VALIDATION_ERROR", http.StatusBadRequest, "input tidak valid").
		WithDetails(map[string]string{"email": "tidak-valid", "field": "nik"})

	writeErr(t, err)

	var rec map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &rec))
	assert.Equal(t, "req-1", rec["requestId"])
	assert.Equal(t, "VALIDATION_ERROR", rec["errorCode"])
	assert.EqualValues(t, 400, rec["status"])
	assert.Equal(t, "INFO", rec["level"])
	assert.Contains(t, rec["error"], assert.AnError.Error())
	assert.Equal(t, map[string]any{"email": "[REDACTED]", "field": "nik"}, rec["details"], "detail ikut redaksi 4.4")
}

func TestWriteError_ServerErrorLogsAtErrorLevel(t *testing.T) {
	buf := captureLog(t)
	writeErr(t, apperror.Internal(assert.AnError))

	var rec map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &rec))
	assert.Equal(t, "ERROR", rec["level"])
	assert.Equal(t, "INTERNAL_ERROR", rec["errorCode"])
}

func TestWriteError_RetryAfterHeader(t *testing.T) {
	captureLog(t)
	for _, key := range []string{"retryAfterSeconds", "retryAfter"} {
		t.Run(key, func(t *testing.T) {
			err := apperror.New(apperror.CodeRateLimited, http.StatusTooManyRequests, "pelan").WithDetails(map[string]string{key: "30"})
			rec := writeErr(t, err)
			assert.Equal(t, "30", rec.Header().Get("Retry-After"))
			assert.NotContains(t, rec.Body.String(), "30", "nilai hanya lewat header")
		})
	}

	rec := writeErr(t, apperror.New(apperror.CodeValidationFailed, http.StatusBadRequest, "x").WithDetails(map[string]string{"retryAfter": "30"}))
	assert.Empty(t, rec.Header().Get("Retry-After"), "hanya untuk 429")
}
