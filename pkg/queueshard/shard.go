// Package queueshard computes the liveness-index shard for a queue entry (ADR-030 Amendment: the hb/hbstale
// liveness index is sharded so the promoter tick and the heartbeat writes no longer contend on a single Valkey
// key). The shard is a pure function of the entry id so Go and every path agree; the entry stores its shard on
// the entry hash at join and later mutations read it back rather than recompute.
package queueshard

// Shard returns the shard index in [0, b) for entryID. b <= 1 collapses to a single shard (index 0), which
// reproduces the pre-sharding single-key behaviour exactly. The hash is Redis-cluster CRC16 (CCITT/XMODEM),
// chosen so the mapping is stable, well-distributed, and trivial to reason about.
func Shard(entryID string, b int) int {
	if b <= 1 {
		return 0
	}
	return int(crc16([]byte(entryID))) % b
}

// crc16Tab is the CCITT (XMODEM) polynomial 0x1021 table used by Redis cluster for hash-slot assignment.
var crc16Tab = func() [256]uint16 {
	var t [256]uint16
	for i := 0; i < 256; i++ {
		crc := uint16(i) << 8
		for j := 0; j < 8; j++ {
			if crc&0x8000 != 0 {
				crc = (crc << 1) ^ 0x1021
			} else {
				crc <<= 1
			}
		}
		t[i] = crc
	}
	return t
}()

func crc16(data []byte) uint16 {
	var crc uint16
	for _, b := range data {
		crc = (crc << 8) ^ crc16Tab[byte(crc>>8)^b]
	}
	return crc
}
