package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCommandTree(t *testing.T) {
	names := map[string]bool{}
	for _, c := range rootCmd.Commands() {
		names[c.Name()] = true
	}
	for _, want := range []string{"api", "migrate", "healthcheck", "worker"} {
		assert.True(t, names[want], "verb %s terdaftar", want)
	}
}

func TestMigrateFlagsAreIndependent(t *testing.T) {
	// up tanpa --steps harus menerapkan semua migrasi; down tanpa --steps hanya 1.
	assert.Equal(t, 0, upSteps)
	assert.Equal(t, 1, downSteps)
	assert.Equal(t, "0", migrateUpCmd.Flags().Lookup("steps").DefValue)
	assert.Equal(t, "1", migrateDownCmd.Flags().Lookup("steps").DefValue)
}
