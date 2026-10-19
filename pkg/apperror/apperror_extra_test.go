package apperror

import (
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestError_MessageWithAndWithoutCause(t *testing.T) {
	assert.Equal(t, "NOT_FOUND: tidak ada", New(CodeNotFound, http.StatusNotFound, "tidak ada").Error())
	err := Wrap(errors.New("db down"), CodeDependencyUnavailable, http.StatusServiceUnavailable, "coba lagi")
	assert.Equal(t, "DEPENDENCY_UNAVAILABLE: coba lagi: db down", err.Error())
}
