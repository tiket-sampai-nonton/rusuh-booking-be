package valueobject

import "errors"

// ErrInvalidSeq is returned when a sequence number is not positive.
var ErrInvalidSeq = errors.New("seq harus > 0")

// Seq is the FIFO sequence number of a queue entry (monotonic, gap-free; INCR in join.lua).
type Seq int64

// NewSeq validates v (> 0).
func NewSeq(v int64) (Seq, error) {
	if v <= 0 {
		return 0, ErrInvalidSeq
	}
	return Seq(v), nil
}

// Int64 returns the underlying value.
func (s Seq) Int64() int64 { return int64(s) }
