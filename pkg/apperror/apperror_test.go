package apperror

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFromKeepsApplicationError(t *testing.T) {
	orig := New(CodeNotFound, http.StatusNotFound, "tidak ditemukan")
	got := From(errors.Join(errors.New("context"), orig))
	assert.Equal(t, CodeNotFound, got.Code)
	assert.Equal(t, http.StatusNotFound, got.HTTPStatus)
}

func TestFromUnknownErrorIsInternal(t *testing.T) {
	cause := errors.New("db down")
	got := From(cause)
	assert.Equal(t, CodeInternal, got.Code)
	assert.Equal(t, http.StatusInternalServerError, got.HTTPStatus)
	assert.ErrorIs(t, got, cause, "cause disimpan untuk log")
	assert.NotContains(t, got.ToEnvelope().Meta.Message, "db down", "cause internal tidak bocor ke klien")
}

// Format error standar (platform R2.8): hanya code dan message; kode error ada di awalan message.
func TestEnvelope_OnlyCodeAndMessage(t *testing.T) {
	e := New(CodeValidationFailed, http.StatusBadRequest, "nik harus 16 digit").WithDetails(map[string]string{"nik": "harus 16 digit"})
	b, err := json.Marshal(e.ToEnvelope())
	require.NoError(t, err)
	assert.JSONEq(t, `{"meta":{"code":400,"message":"VALIDATION_ERROR: nik harus 16 digit"},"data":null}`, string(b))
}

func TestClientMessage(t *testing.T) {
	assert.Equal(t, "NOT_FOUND: tidak ada", New(CodeNotFound, http.StatusNotFound, "tidak ada").ClientMessage())

	internal := Wrap(errors.New("pq: password=hunter2"), CodeInternal, http.StatusInternalServerError, "detail developer")
	assert.Equal(t, "INTERNAL_ERROR: terjadi kesalahan internal", internal.ClientMessage())
	assert.Equal(t, "INTERNAL_ERROR: terjadi kesalahan internal", From(errors.New("boom")).ClientMessage())
}

func TestWithDetailsDoesNotMutateOriginal(t *testing.T) {
	base := New(CodeRateLimited, http.StatusTooManyRequests, "pelan")
	_ = base.WithDetails(map[string]string{"retryAfterSeconds": "5"})
	assert.Nil(t, base.Details)
}
