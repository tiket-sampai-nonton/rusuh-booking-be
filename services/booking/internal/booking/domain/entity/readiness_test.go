package entity

import (
	"testing"
	"time"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/valueobject"
)

func TestNewReadinessRequiresAllDependencies(t *testing.T) {
	pg, _ := valueobject.NewDependencyName("postgres")
	mq, _ := valueobject.NewDependencyName("rabbitmq")
	at := time.Unix(0, 0)

	if r := NewReadiness("booking", []DependencyStatus{{Name: pg, Reachable: true}}, at); !r.Ready {
		t.Fatal("all reachable must be ready")
	}
	r := NewReadiness("booking", []DependencyStatus{{Name: pg, Reachable: true}, {Name: mq}}, at)
	if r.Ready {
		t.Fatal("one unreachable dependency must make the service not ready")
	}
}
