package valkey

import (
	"errors"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/domain/valueobject"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/queue/port/outbound"
)

// strReply is a bulk-string element of a me.lua reply.
type strReply string

func (r strReply) ToString() (string, error) { return string(r), nil }
func (r strReply) AsInt64() (int64, error)   { return strconv.ParseInt(string(r), 10, 64) }

// badReply is an element of a type decodeMyEntry cannot read (an error reply or a nested array).
type badReply struct{}

func (badReply) ToString() (string, error) { return "", errors.New("not a string") }
func (badReply) AsInt64() (int64, error)   { return 0, errors.New("not an integer") }

func replies(vals ...meReply) []meReply { return vals }

// C8.9 (PF-P2-3): decodeMyEntry accepts the four known tags and rejects anything else, and reports every element it
// cannot read instead of dropping it.
func TestDecodeMyEntry(t *testing.T) {
	t.Run("known tags without an entry", func(t *testing.T) {
		for _, tag := range []outbound.MyEntryTag{outbound.MyEntryAbsent, outbound.MyEntryInvalid, outbound.MyEntryNotFound} {
			got, err := decodeMyEntry(replies(strReply(tag)))
			require.NoError(t, err, string(tag))
			assert.Equal(t, tag, got.Tag)
			assert.Equal(t, outbound.MyEntryResult{Tag: tag}, got)
		}
	})

	t.Run("OK decodes the entry and the live position", func(t *testing.T) {
		got, err := decodeMyEntry(replies(
			strReply("OK"), strReply("e1"), strReply("2"),
			strReply("visitorId"), strReply("v1"), strReply("status"), strReply("WAITING"), strReply("seq"), strReply("5"),
		))
		require.NoError(t, err)
		assert.Equal(t, outbound.MyEntryOK, got.Tag)
		assert.EqualValues(t, 2, got.Position)
		assert.Equal(t, "e1", got.Entry.ID)
		assert.Equal(t, "v1", got.Entry.VisitorID)
		assert.Equal(t, valueobject.StatusWaiting, got.Entry.Status)
		assert.EqualValues(t, 5, got.Entry.Seq.Int64())
	})

	failures := []struct {
		name string
		arr  []meReply
	}{
		{"empty reply", replies()},
		{"tag cannot be read", replies(badReply{})},
		{"unknown tag", replies(strReply("WAITING"))},
		{"empty tag", replies(strReply(""))},
		{"OK without position", replies(strReply("OK"), strReply("e1"))},
		{"OK with an odd field element", replies(strReply("OK"), strReply("e1"), strReply("2"), strReply("visitorId"))},
		{"OK with a position that is not an integer", replies(strReply("OK"), strReply("e1"), strReply("x"))},
		{"OK with an unreadable field name", replies(strReply("OK"), strReply("e1"), strReply("2"), badReply{}, strReply("v"))},
		{"OK with an unreadable field value", replies(strReply("OK"), strReply("e1"), strReply("2"), strReply("visitorId"), badReply{})},
		{"OK with an unreadable entry id", replies(strReply("OK"), badReply{}, strReply("2"))},
	}
	for _, tt := range failures {
		t.Run(tt.name, func(t *testing.T) {
			_, err := decodeMyEntry(tt.arr)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "valkey: me.lua")
		})
	}
}
