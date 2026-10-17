package clock

import (
	"testing"
	"time"
)

func TestFixedReturnsSameInstant(t *testing.T) {
	at := time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC)
	c := Fixed{T: at}

	if !c.Now().Equal(at) || !c.Now().Equal(c.Now()) {
		t.Fatal("Fixed must always return the configured instant")
	}
}

func TestSystemIsUTC(t *testing.T) {
	if loc := (System{}).Now().Location(); loc != time.UTC {
		t.Fatalf("System clock location = %v, want UTC", loc)
	}
}
