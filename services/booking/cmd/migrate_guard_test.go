package main

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeCounter struct {
	n     int
	err   error
	calls int
}

func (f *fakeCounter) CountLiveAdmissions(context.Context, string) (int, error) {
	f.calls++
	return f.n, f.err
}

func TestGuardLiveAdmissions(t *testing.T) {
	tests := []struct {
		name         string
		n            int
		pending      bool
		allow        bool
		err          error
		wantErr      bool
		wantMsg      string
		wantCountRun bool
	}{
		{"live rows without flag refuse", 3, true, false, nil, true, "3 live admissions", true},
		{"live rows with flag apply", 3, true, true, nil, false, "", false},
		{"no live rows apply", 0, true, false, nil, false, "", true},
		{"already applied: guard does not run", 3, false, false, nil, false, "", false},
		{"count error is returned", 0, true, false, errors.New("boom"), true, "boom", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &fakeCounter{n: tt.n, err: tt.err}
			err := guardLiveAdmissions(context.Background(), f, "booking", tt.pending, tt.allow)
			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantMsg)
			} else {
				require.NoError(t, err)
			}
			assert.Equal(t, tt.wantCountRun, f.calls > 0)
		})
	}
}
