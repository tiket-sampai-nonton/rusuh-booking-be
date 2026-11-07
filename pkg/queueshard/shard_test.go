package queueshard

import "testing"

func TestShard_InRangeAndDeterministic(t *testing.T) {
	cases := []struct {
		name    string
		entryID string
		b       int
	}{
		{"simple id, 16 shards", "entry-1", 16},
		{"uuid-like, 16 shards", "550e8400-e29b-41d4-a716-446655440000", 16},
		{"single shard collapses to 0", "anything", 1},
		{"max shards", "entry-xyz", 256},
		{"empty id", "", 16},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Shard(tc.entryID, tc.b)
			if got < 0 || got >= tc.b {
				t.Fatalf("Shard(%q, %d) = %d, want in [0,%d)", tc.entryID, tc.b, got, tc.b)
			}
			if again := Shard(tc.entryID, tc.b); again != got {
				t.Fatalf("Shard not deterministic: %d then %d", got, again)
			}
		})
	}
}

func TestShard_B1AlwaysZero(t *testing.T) {
	for _, id := range []string{"a", "b", "c", "entry-9999", ""} {
		if got := Shard(id, 1); got != 0 {
			t.Fatalf("Shard(%q, 1) = %d, want 0", id, got)
		}
	}
}

func TestShard_NonPositiveBDefaultsToSingleShard(t *testing.T) {
	for _, b := range []int{0, -1, -16} {
		if got := Shard("entry-1", b); got != 0 {
			t.Fatalf("Shard with b=%d = %d, want 0 (single shard)", b, got)
		}
	}
}

func TestShard_DistributesAcrossShards(t *testing.T) {
	// A spread check: distinct ids must not all collapse into one shard at B=16.
	const b = 16
	seen := map[int]bool{}
	for i := 0; i < 1000; i++ {
		seen[Shard("entry-"+itoa(i), b)] = true
	}
	if len(seen) < b/2 {
		t.Fatalf("poor shard distribution: only %d of %d shards used", len(seen), b)
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(buf[pos:])
}

func TestCRC16_KnownVector(t *testing.T) {
	// Redis cluster CRC16 (CCITT/XMODEM) of "123456789" is 0x31C3.
	if got := crc16([]byte("123456789")); got != 0x31C3 {
		t.Fatalf("crc16(\"123456789\") = %#x, want 0x31C3", got)
	}
}
