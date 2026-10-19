package postgres

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNew_FailsFast(t *testing.T) {
	_, err := New(context.Background(), Config{})
	assert.ErrorContains(t, err, "POSTGRES_HOST")

	c := validConfig()
	c.Port = 1 // tidak ada server
	_, err = New(context.Background(), c)
	assert.ErrorContains(t, err, "ping")
	assert.NotContains(t, err.Error(), "p%40ss", "password tidak bocor ke pesan error")
}
