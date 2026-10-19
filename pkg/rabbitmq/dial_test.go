package rabbitmq

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDial_FailsFast(t *testing.T) {
	_, err := Dial(Config{})
	assert.ErrorContains(t, err, "RABBITMQ_URL")

	c := validCfg()
	c.URL = "amqp://guest:secret@127.0.0.1:1/"
	_, err = Dial(c)
	assert.ErrorContains(t, err, "dial")
	assert.NotContains(t, err.Error(), "secret")
}
