package tcpcheck

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/valueobject"
)

func TestCheckAll(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	closed, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	closedAddr := closed.Addr().String()
	_ = closed.Close()

	up, _ := valueobject.NewDependencyName("up")
	down, _ := valueobject.NewDependencyName("down")
	c := New([]Target{{Name: up, Addr: ln.Addr().String()}, {Name: down, Addr: closedAddr}}, 500*time.Millisecond)

	got := c.CheckAll(context.Background())

	if len(got) != 2 || !got[0].Reachable || got[1].Reachable || got[1].Reason == "" {
		t.Fatalf("got %+v", got)
	}
}
