package valueobject

import (
	"errors"
	"testing"
)

func TestNewDependencyName(t *testing.T) {
	if n, err := NewDependencyName("postgres"); err != nil || n.String() != "postgres" {
		t.Fatalf("valid name rejected: %v", err)
	}
	for _, bad := range []string{"", "Postgres", "pg sql", "1abc"} {
		if _, err := NewDependencyName(bad); !errors.Is(err, ErrInvalidDependencyName) {
			t.Errorf("NewDependencyName(%q) error = %v, want ErrInvalidDependencyName", bad, err)
		}
	}
}
