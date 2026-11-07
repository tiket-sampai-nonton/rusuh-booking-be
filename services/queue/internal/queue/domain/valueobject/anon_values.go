package valueobject

// ZoneCounts returns the zone boundaries in ENTRIES (ADR-030, R05.1): G = ceil(F/M) front-most live entries are
// guaranteed by free stock, P = ceil((F+A+H)/M) may still be served if reserved/held units come back. M <= 0 means
// the default quantity guarantee of 3.
func ZoneCounts(free, allocated, held, maxPerOrder int) (g, p int64) {
	m := maxPerOrder
	if m <= 0 {
		m = 3
	}
	ceil := func(n int) int64 {
		if n <= 0 {
			return 0
		}
		return int64((n + m - 1) / m)
	}
	return ceil(free), ceil(free + allocated + held)
}

// ZoneForSeq is the single zone rule over SEQ (ADR-030 Amendment: the draw was dropped, so seq is the order):
// GUARANTEED iff seq <= guaranteedUntilSeq > 0, POSSIBLE iff not guaranteed and seq <= possibleUntilSeq > 0,
// else NOT_ENOUGH.
func ZoneForSeq(seq, guaranteedUntilSeq, possibleUntilSeq int64) Zone {
	switch {
	case guaranteedUntilSeq > 0 && seq <= guaranteedUntilSeq:
		return ZoneGuaranteed
	case possibleUntilSeq > 0 && seq <= possibleUntilSeq:
		return ZonePossible
	default:
		return ZoneNotEnough
	}
}

// NextHeartbeatMs is the server-controlled heartbeat interval (R03.1): base ±20% jitter (jitter in [0,1)), or the
// near interval when the entry is within nearSeqs of the head. seq <= 0 (unknown) uses the base interval.
func NextHeartbeatMs(baseMs, nearMs, nearSeqs, seq, headSeq int64, jitter float64) int64 {
	if seq > 0 && headSeq > 0 && seq-headSeq < nearSeqs {
		return nearMs
	}
	return baseMs + int64(float64(baseMs)*0.4*(jitter-0.5))
}
