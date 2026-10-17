package apperror

import (
	"errors"
	"net/http"
	"testing"
)

func TestFromKeepsApplicationError(t *testing.T) {
	orig := New(CodeNotFound, http.StatusNotFound, "tidak ditemukan")
	wrapped := errors.Join(errors.New("context"), orig)

	got := From(wrapped)

	if got.Code != CodeNotFound || got.HTTPStatus != http.StatusNotFound {
		t.Fatalf("From() = %+v, want NOT_FOUND/404", got)
	}
}

func TestFromUnknownErrorIsInternal(t *testing.T) {
	cause := errors.New("db down")

	got := From(cause)

	if got.Code != CodeInternal || got.HTTPStatus != http.StatusInternalServerError {
		t.Fatalf("From() = %+v, want INTERNAL_ERROR/500", got)
	}
	if !errors.Is(got, cause) {
		t.Fatal("cause must be preserved for logging")
	}
	if got.ToEnvelope("").Error.Message == cause.Error() {
		t.Fatal("internal cause must not leak to the client")
	}
}

func TestEnvelopeCarriesRequestIDAndDetails(t *testing.T) {
	e := New(CodeValidationFailed, http.StatusBadRequest, "input tidak valid").
		WithDetails(map[string]string{"nik": "harus 16 digit"})

	env := e.ToEnvelope("req-1")

	if env.Error.RequestID != "req-1" || env.Error.Details["nik"] == "" {
		t.Fatalf("envelope = %+v", env)
	}
}
