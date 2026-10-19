package migrate

import (
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
)

func TestCheckDrift(t *testing.T) {
	migs, err := Load(fstest.MapFS{
		"m/0001_a.up.sql": {Data: []byte("x")}, "m/0001_a.down.sql": {Data: []byte("y")},
	}, "m")
	assert.NoError(t, err)
	r := &Runner{schema: "s", migrations: migs}
	assert.NoError(t, r.checkDrift(map[int]applied{1: {checksum: migs[0].Checksum}}))
	assert.ErrorContains(t, r.checkDrift(map[int]applied{1: {checksum: "other"}}), "berubah")
	assert.ErrorContains(t, r.checkDrift(map[int]applied{2: {checksum: "x"}}), "tidak ada di binary")
}
