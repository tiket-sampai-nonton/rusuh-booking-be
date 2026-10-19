package rabbitmq

import (
	"errors"
	"fmt"
	"time"
)

// PermanentError marks a failure that retrying cannot fix (payload rusak, versi schema tidak dikenal).
// Pesan langsung masuk DLQ.
type PermanentError struct{ Err error }

func (e *PermanentError) Error() string { return "permanent: " + e.Err.Error() }
func (e *PermanentError) Unwrap() error { return e.Err }

// Permanent wraps err as permanent.
func Permanent(err error) error { return &PermanentError{Err: err} }

// IsPermanent reports whether err (or a wrapped error) is permanent.
func IsPermanent(err error) bool {
	var p *PermanentError
	return errors.As(err, &p)
}

// Action is what the consumer does with a failed delivery.
type Action int

// Actions.
const (
	ActionAck Action = iota
	ActionRetry
	ActionDeadLetter
)

// Decision is the outcome of Decide.
type Decision struct {
	Action Action
	Queue  string // target queue for retry or DLQ
	Delay  time.Duration
}

// RetryQueueName returns "<queue>.retry.<delay>" (mis. booking.payment-succeeded.retry.5s).
func RetryQueueName(queue string, d time.Duration) string {
	return fmt.Sprintf("%s.retry.%s", queue, shortDuration(d))
}

// DLQName returns "<queue>.dlq".
func DLQName(queue string) string { return queue + ".dlq" }

// Decide maps (attempt yang baru gagal, err) ke tindakan. attempt dimulai dari 1.
// Pola: percobaan 1 gagal → retry tier 1, ..., percobaan ke-(MaxRetries+1) gagal → DLQ.
// Tier di luar daftar delay memakai delay terakhir.
func Decide(queue string, attempt int, err error, maxRetries int, delays []time.Duration) Decision {
	if err == nil {
		return Decision{Action: ActionAck}
	}
	if IsPermanent(err) || attempt > maxRetries || len(delays) == 0 {
		return Decision{Action: ActionDeadLetter, Queue: DLQName(queue)}
	}
	idx := attempt - 1
	if idx >= len(delays) {
		idx = len(delays) - 1
	}
	if idx < 0 {
		idx = 0
	}
	d := delays[idx]
	return Decision{Action: ActionRetry, Queue: RetryQueueName(queue, d), Delay: d}
}

func shortDuration(d time.Duration) string {
	switch {
	case d%time.Hour == 0:
		return fmt.Sprintf("%dh", d/time.Hour)
	case d%time.Minute == 0:
		return fmt.Sprintf("%dm", d/time.Minute)
	case d%time.Second == 0:
		return fmt.Sprintf("%ds", d/time.Second)
	default:
		return fmt.Sprintf("%dms", d/time.Millisecond)
	}
}
