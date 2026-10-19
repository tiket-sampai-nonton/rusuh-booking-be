package valkey

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNew_FailsFast(t *testing.T) {
	_, err := New(context.Background(), Config{Server: "nope"})
	assert.ErrorContains(t, err, "VALKEY_SERVER")
	_, err = New(context.Background(), Config{Server: `[{"host":"127.0.0.1","port":1}]`, TLS: true})
	assert.Error(t, err)
}
