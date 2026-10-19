package dotenv

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestBool(t *testing.T) {
	t.Setenv("RBT_FLAG", "true")
	v, err := Bool("RBT_FLAG", false)
	assert.NoError(t, err)
	assert.True(t, v)

	t.Setenv("RBT_FLAG", "maybe")
	_, err = Bool("RBT_FLAG", false)
	assert.Error(t, err)

	v, err = Bool("RBT_UNSET_FLAG", true)
	assert.NoError(t, err)
	assert.True(t, v)
}

func TestReader_CollectsAllErrors(t *testing.T) {
	t.Setenv("RBT_PORT", "eighty")
	t.Setenv("RBT_TIMEOUT", "soon")
	t.Setenv("RBT_TLS", "yes please")
	t.Setenv("RBT_NAME", "booking")

	var r Reader
	assert.Equal(t, 8080, r.Int("RBT_PORT", 8080))
	assert.Equal(t, time.Second, r.Duration("RBT_TIMEOUT", time.Second))
	assert.False(t, r.Bool("RBT_TLS", false))
	assert.Equal(t, "booking", r.String("RBT_NAME", "x"))

	err := r.Err()
	assert.ErrorContains(t, err, "RBT_PORT")
	assert.ErrorContains(t, err, "RBT_TIMEOUT")
	assert.ErrorContains(t, err, "RBT_TLS")
}

func TestReader_NoErrors(t *testing.T) {
	var r Reader
	assert.Equal(t, 3, r.Int("RBT_UNSET_INT", 3))
	assert.NoError(t, r.Err())
}
