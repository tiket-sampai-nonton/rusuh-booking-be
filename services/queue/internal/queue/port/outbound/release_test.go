package outbound_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	mock_outbound "github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/_mock/queue/port/outbound"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/port/outbound"
)

func TestReleaseTerminal_ReleasesTheGrantedIDForTerminalSkipsOnly(t *testing.T) {
	ctrl := gomock.NewController(t)
	booking := mock_outbound.NewMockIBookingClient(ctrl)
	ctx := context.Background()

	skips := []outbound.PromoteSkip{
		{EntryID: "e-left", Status: "LEFT"},
		{EntryID: "e-same", Status: "ADMITTED_SAME"},
		{EntryID: "e-nothead", Status: "NOT_HEAD"},
		{EntryID: "e-other", Status: "ADMITTED_OTHER", AllocationID: "alloc-STORED"}, // must never release the stored id
		{EntryID: "e-noalloc", Status: "ENDED"},                                      // no granted id known: nothing to release
	}
	granted := map[string]string{"e-left": "a-left", "e-same": "a-same", "e-nothead": "a-nh", "e-other": "a-other-granted"}
	booking.EXPECT().ReleaseAllocation(ctx, "a-left", "QUEUE_ENTRY_GONE").Return(nil)
	booking.EXPECT().ReleaseAllocation(ctx, "a-other-granted", "QUEUE_ENTRY_GONE").Return(nil)

	n := outbound.ReleaseTerminal(ctx, booking, slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)), skips, granted)
	assert.Equal(t, 2, n)
}

func TestReleaseTerminal_AFailedReleaseIsLoggedAndTheNextSkipIsStillProcessed(t *testing.T) {
	ctrl := gomock.NewController(t)
	booking := mock_outbound.NewMockIBookingClient(ctrl)
	ctx := context.Background()
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))

	booking.EXPECT().ReleaseAllocation(ctx, "a1", gomock.Any()).Return(errors.New("booking down"))
	booking.EXPECT().ReleaseAllocation(ctx, "a2", gomock.Any()).Return(nil)
	n := outbound.ReleaseTerminal(ctx, booking, logger,
		[]outbound.PromoteSkip{{EntryID: "e1", Status: "LEFT"}, {EntryID: "e2", Status: "MISSED"}},
		map[string]string{"e1": "a1", "e2": "a2"})
	assert.Equal(t, 1, n, "hanya yang berhasil dihitung")
	require.Contains(t, logs.String(), "a1", "id alokasi dicatat untuk investigasi")
	assert.NotContains(t, logs.String(), "@", "tidak ada email/PII di log")
}

func TestReleaseTerminal_NoSkipsMakesNoCalls(t *testing.T) {
	ctrl := gomock.NewController(t)
	booking := mock_outbound.NewMockIBookingClient(ctrl)
	assert.Equal(t, 0, outbound.ReleaseTerminal(context.Background(), booking, nil, nil, nil))
}
