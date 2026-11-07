//go:build integration

package worker_test

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/clock"
	pkgvalkey "github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/valkey"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/application/usecase"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/domain/dto"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/domain/entity"
	queuevalkey "github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/infrastructure/valkey"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/infrastructure/worker"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/port/inbound"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/port/outbound"
)

// mustValkey fails (never skips) when TEST_VALKEY_SERVER is unset or the server is not reachable (plan R9-2).
func mustValkey(t *testing.T) string {
	t.Helper()
	server := os.Getenv("TEST_VALKEY_SERVER")
	if server == "" {
		t.Fatal("TEST_VALKEY_SERVER wajib diset untuk test integrasi QSH (tanpa skip senyap)")
	}
	return server
}

// twoStores returns two independent real QueueStore clients that share one key prefix, i.e. two worker/API
// processes talking to the same Valkey (the second is the "competitor").
func twoStores(t *testing.T) (a, b *queuevalkey.QueueStore) {
	t.Helper()
	server := mustValkey(t)
	prefix := fmt.Sprintf("t27%d", time.Now().UnixNano()%1_000_000_000_000)
	mk := func() *queuevalkey.QueueStore {
		c, err := pkgvalkey.New(context.Background(), pkgvalkey.Config{Server: server, PrefixEnv: prefix})
		require.NoError(t, err)
		t.Cleanup(c.Close)
		return queuevalkey.NewQueueStore(c.VK(), c, clock.System{}, 16)
	}
	return mk(), mk()
}

type itTokens struct{}

func (itTokens) IssueAdmission(string, string, string, time.Time) (string, time.Time, error) {
	return "adm", time.Now().Add(time.Minute), nil
}
func (itTokens) IssueVisitor(outbound.VisitorClaim, time.Time) (string, error) { return "vt", nil }
func (itTokens) IssueQueue(string, string, time.Time) (string, error)          { return "qt", nil }

type itLimiter struct{}

func (itLimiter) Allow(context.Context, string, int, time.Duration) (bool, error) { return true, nil }

type itAudit struct{}

func (itAudit) Append(context.Context, outbound.AuditRecord) error { return nil }

// newJoinUC builds the real JoinQueue over store with an injected instant Sleep.
func newJoinUC(store outbound.IQueueStore, booking outbound.IBookingClient, settings *fakeSettings, metrics outbound.IPromoterMetrics) *usecase.JoinQueue {
	return usecase.NewJoinQueue(usecase.JoinDeps{
		Store: store, Settings: settings, Booking: booking, Limiter: itLimiter{}, Tokens: itTokens{},
		Audit: itAudit{}, Clock: clock.System{},
		Sleep:  func(context.Context, time.Duration) error { return nil },
		Jitter: func() time.Duration { return time.Millisecond },
	}, usecase.JoinSettings{
		JoinPerIP: 1000, JoinPerIPWindow: time.Minute,
		ClaimWindow: 3 * time.Minute, StatusURLTemplate: "/s/", AdmissionLeaseTTL: 10 * time.Second,
	}).WithMetrics(metrics)
}

// joinAs joins an anonymous, untokened visitor; the join carries no buyer data (ADR-030).
func joinAs(ctx context.Context, uc *usecase.JoinQueue, _ int) (dto.QueueEntry, error) {
	return uc.Join(ctx, inbound.JoinInput{EventID: "ev", ClientIP: "10.0.0.1"})
}

func settingsFor(capacity int) *fakeSettings {
	return &fakeSettings{s: entity.EventSettings{EventID: "ev", ActiveSlotCapacity: capacity, OneActivePerVisitor: true}}
}

func newIntegrationPromoter(store outbound.IQueueStore, booking outbound.IBookingClient, settings *fakeSettings, metrics outbound.IPromoterMetrics, clk clock.IClock) *worker.Promoter {
	return worker.NewPromoter(store, settings, booking, &fakePub{}, itAudit{}, clk, nil, worker.PromoterTuning{BatchMax: 200}).WithMetrics(metrics)
}

// realStatus reads the entry status straight from the real store.
func realStatus(t *testing.T, s *queuevalkey.QueueStore, id string) string {
	t.Helper()
	e, ok, err := s.GetEntry(context.Background(), "ev", id)
	require.NoError(t, err)
	if !ok {
		return "<none>"
	}
	return string(e.Status)
}
