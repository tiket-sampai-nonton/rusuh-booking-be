package worker

// headRec is the in-memory poison-head detection record of one event (design §5.4a.3 C8 "Detection", rev 8).
// It only labels and counts: it never changes a decision (P20). It is lost on lease handover or restart, which can
// only under-count.
type headRec struct {
	headID        string
	noSupplyTicks int // length of the unbroken run of head NO_SUPPLY_NOW replies
	adoptedTicks  int // ticks within that run in which a later entry was adopted
}

// tickObs is what one evaluated tick observed about the head of waiting.
type tickObs struct {
	headID   string // TopWaiting(free)[0] read at tick start (not necessarily the first entry sent to Booking)
	reached  bool   // the head reached a ReserveBatch reply this tick
	noSupply bool   // the head's reply was NO_SUPPLY_NOW
	adopted  bool   // a later entry of the same batch was adopted (promoted GRANTED after the head)
}

// headEffects are the counters and logs implied by the record after a tick, as pure functions of the record.
type headEffects struct {
	Repeat        bool // queue_promote_head_no_supply_repeat_total moves
	RepeatAdopted bool // ... with adopted="true" (else "false")
	Suspect       bool // queue_promote_fifo_adopted_total uses suspect="true"
	Warn          bool // log WARN (eventId/entryId only)
}

// nextRec applies rows 1-5 of the C8 table for an evaluated tick (row 0, empty waiting, is the caller's: drop the
// record). n is QUEUE_NO_SUPPLY_SUSPECT_TICKS (>= 2).
func nextRec(rec *headRec, obs tickObs, n int) (*headRec, headEffects) {
	var eff headEffects
	// row 1: a different head starts a new run
	if rec == nil || rec.headID != obs.headID {
		rec = &headRec{headID: obs.headID}
	} else {
		cp := *rec
		rec = &cp
	}
	// row 2: the head did not reach a reply: untouched
	if !obs.reached {
		return rec, eff
	}
	if !obs.noSupply {
		// row 5: any other reply ends the run
		rec.noSupplyTicks, rec.adoptedTicks = 0, 0
		return rec, eff
	}
	// rows 3 and 4
	rec.noSupplyTicks++
	if obs.adopted {
		rec.adoptedTicks++
		eff.Suspect = rec.adoptedTicks >= n
	}
	if rec.noSupplyTicks >= n {
		eff.Repeat = true
		eff.RepeatAdopted = rec.adoptedTicks >= n
	}
	eff.Warn = rec.noSupplyTicks == n || (rec.noSupplyTicks > n && rec.noSupplyTicks%100 == 0)
	return rec, eff
}
