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
	assert.NotEqual(t, cause.Error(), got.ToEnvelope("").Meta.Message, "cause internal tidak bocor ke klien")
}

func TestEnvelope_MatchesSRSShape(t *testing.T) {
	e := New(CodeValidationFailed, http.StatusBadRequest, "input tidak valid").WithDetails(map[string]string{"nik": "harus 16 digit"})
	b, err := json.Marshal(e.ToEnvelope("req-1"))
	require.NoError(t, err)
	assert.JSONEq(t, `{"meta":{"code":400,"message":"input tidak valid","requestId":"req-1","errorCode":"VALIDATION_ERROR","details":{"nik":"harus 16 digit"}},"data":null}`, string(b))
}

func TestWithDetailsDoesNotMutateOriginal(t *testing.T) {
	base := New(CodeRateLimited, http.StatusTooManyRequests, "pelan")
	_ = base.WithDetails(map[string]string{"retryAfterSeconds": "5"})
	assert.Nil(t, base.Details)
}
