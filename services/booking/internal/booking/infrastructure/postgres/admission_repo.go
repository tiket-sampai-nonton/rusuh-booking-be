package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	pkgpg "github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/postgres"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/entity"
	vo "github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/valueobject"
	inbound "github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/inbound"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/outbound"
)

// AdmissionRepo manages admissions.
type AdmissionRepo struct{ pool *pgxpool.Pool }

// NewAdmissionRepo returns the repository.
func NewAdmissionRepo(pool *pgxpool.Pool) *AdmissionRepo { return &AdmissionRepo{pool: pool} }

const admissionCols = `id::text, event_id::text, queue_entry_id::text, queue_seq, visitor_id::text,
	buyer_nik_hash, COALESCE(buyer_email_enc, ''), COALESCE(buyer_nik_masked, ''), units, status,
	COALESCE(admission_jti::text, ''), attendee_checks, edit_count, COALESCE(account_id::text, ''),
	cfg_selection_ttl_seconds, cfg_payment_ttl_seconds, cfg_submit_grace_seconds,
	cfg_edit_enabled, cfg_max_edits, cfg_allow_mixed_categories,
	reserved_at, expires_at, outstanding_units`

func scanAdmission(row pgx.Row) (entity.Admission, error) {
	var a entity.Admission
	err := row.Scan(
		&a.ID, &a.EventID, &a.QueueEntryID, &a.QueueSeq, &a.VisitorID,
		&a.BuyerNIKHash, &a.BuyerEmailEnc, &a.BuyerNIKMasked, &a.Units, &a.Status,
		&a.AdmissionJTI, &a.AttendeeChecks, &a.EditCount, &a.AccountID,
		&a.CfgSelectionTTLSeconds, &a.CfgPaymentTTLSeconds, &a.CfgSubmitGraceSeconds,
		&a.CfgEditEnabled, &a.CfgMaxEdits, &a.CfgAllowMixedCategories,
		&a.ReservedAt, &a.ExpiresAt, &a.OutstandingUnits,
	)
	return a, err
}

// GetByID returns one admission or inbound.ErrNotFound.
func (r *AdmissionRepo) GetByID(ctx context.Context, id string) (entity.Admission, error) {
	a, err := scanAdmission(r.pool.QueryRow(ctx, `SELECT `+admissionCols+` FROM booking.admissions WHERE id = $1::uuid`, id))
	return mapAdmissionErr(a, err)
}

// FindActiveByVisitor returns the active admission for a visitor on an event.
func (r *AdmissionRepo) FindActiveByVisitor(ctx context.Context, eventID, visitorID string) (entity.Admission, error) {
	a, err := scanAdmission(r.pool.QueryRow(ctx, `SELECT `+admissionCols+`
		FROM booking.admissions
		WHERE event_id = $1::uuid AND visitor_id = $2::uuid
		  AND status IN ('RESERVED','ACTIVE','EDITABLE')`, eventID, visitorID))
	return mapAdmissionErr(a, err)
}

// FindActiveByEvent is an alias used by current-session recovery.
func (r *AdmissionRepo) FindActiveByEvent(ctx context.Context, eventID, visitorID string) (entity.Admission, error) {
	return r.FindActiveByVisitor(ctx, eventID, visitorID)
}

// Activate flips a RESERVED admission to ACTIVE by its one-time jti and starts the selection clock:
// the session deadline becomes activation time + the snapshotted selection TTL. The guard still reads the old
// (claim-window) deadline, so the claim window keeps bounding RESERVED -> ACTIVE.
func (r *AdmissionRepo) Activate(ctx context.Context, jti string, at time.Time) (entity.Admission, error) {
	// jti in the admission token is the admission's primary key (id), which Queue knows as allocationId.
	a, err := scanAdmission(r.pool.QueryRow(ctx, `
		UPDATE booking.admissions
		   SET status = 'ACTIVE', claimed_at = $2,
		       expires_at = $2 + make_interval(secs => cfg_selection_ttl_seconds)
		 WHERE id = $1::uuid AND status = 'RESERVED' AND expires_at > $2
		 RETURNING `+admissionCols, jti, at))
	if errors.Is(err, pgx.ErrNoRows) || pkgpg.IsCode(err, "22P02") {
		return entity.Admission{}, inbound.ErrAdmissionToken
	}
	return mapAdmissionErr(a, err)
}

// batchRow is an admission of the batch that already exists. It is read under the batch lock (R18), so it is stable.
type batchRow struct {
	id, status, endReason string
	units                 int
	expiresAt             time.Time
}

// lapsedUnclaimed reports a RESERVED row that lapsed unclaimed: Queue never admitted its entry, so it is granted again
// (D-C6.6). Every other existing row is replayed.
func (row batchRow) lapsedUnclaimed() bool {
	return row.status == "EXPIRED" && row.endReason == "UNCLAIMED"
}

// pendingEntry is an entry of the batch that needs a new grant decision. regrantID is the lapsed row it re-grants.
type pendingEntry struct {
	entry     int
	regrantID string
}

// batchGrant is a granted entry of the batch with its units and the lapsed row it re-grants, if any.
type batchGrant struct {
	entry     int
	units     int
	regrantID string
}

// landedRow is an admission written by the batch: its id and claim deadline.
type landedRow struct {
	id        string
	expiresAt time.Time
}

// supplyCounters is the event_supply row as read under its lock, with the sales limit of the event.
type supplyCounters struct {
	total, alloc, held, sold, maxPerOrder int
}

// ReserveBatch reserves one admission per entry in a single transaction (invariant #2). The batch locks the event_supply
// row once (C7.2): the grants are computed in Go, in entry order, against that locked row, then written with one aggregate
// supply update and one bulk insert. An entry that still carries a buyer NIK hash keeps its per-entry quota check under the
// same lock, in its own savepoint.
func (r *AdmissionRepo) ReserveBatch(ctx context.Context, eventID string, entries []outbound.ReserveEntry, at time.Time) ([]outbound.ReserveOutcome, error) {
	var out []outbound.ReserveOutcome
	err := pkgpg.WithTx(ctx, r.pool, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		// The sales window and cancellation are read once per batch, before any entry can be granted (AC-C4.4).
		gate, ended, err := saleGate(ctx, tx, eventID, at)
		if err != nil {
			return err
		}
		if gate != "" {
			out = make([]outbound.ReserveOutcome, 0, len(entries))
			for _, e := range entries {
				out = append(out, outbound.ReserveOutcome{QueueEntryID: e.QueueEntryID, Units: e.Units, Outcome: gate})
			}
			return nil
		}
		out, err = decideBatch(ctx, tx, eventID, entries, ended, at)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("admissions: reserve batch: %w", err)
	}
	return out, nil
}

// uuidText is the canonical 8-4-4-4-12 hex form that Queue sends. Any other value is a malformed entry (R24).
var uuidText = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// entryFault reports an entry that cannot be written on its own, with the outcome the per-entry path gave it (R24): a
// malformed id failed the insert (NO_SUPPLY_NOW), and a queue_seq or units outside the admission CHECK gave SOLD_OUT.
func entryFault(e outbound.ReserveEntry) (outcome string, faulty bool) {
	if !uuidText.MatchString(e.QueueEntryID) || !uuidText.MatchString(e.VisitorID) || !uuidText.MatchString(e.AdmissionJTI) ||
		(e.AccountID != "" && !uuidText.MatchString(e.AccountID)) {
		return "NO_SUPPLY_NOW", true
	}
	if e.Seq < 1 || e.Units < 1 {
		return "SOLD_OUT", true
	}
	return "", false
}

// decideBatch answers the entries of a batch in order, inside tx. An entry that cannot be written on its own is answered
// alone (R24). The admission rows of the others are locked once, in id order, before any supply lock (R18), and that read
// is their replay lookup. A queue entry that repeats in the batch gets the outcome of its first occurrence.
func decideBatch(ctx context.Context, tx pgx.Tx, eventID string, entries []outbound.ReserveEntry, ended bool, at time.Time) ([]outbound.ReserveOutcome, error) {
	out := make([]outbound.ReserveOutcome, len(entries))
	first := make(map[string]int, len(entries))
	var checked []int
	for i, e := range entries {
		out[i] = outbound.ReserveOutcome{QueueEntryID: e.QueueEntryID, Units: e.Units}
		if _, dup := first[e.QueueEntryID]; dup {
			continue
		}
		first[e.QueueEntryID] = i
		if fault, faulty := entryFault(e); faulty {
			out[i].Outcome = fault
			if ended {
				out[i].Outcome = "SOLD_OUT"
			}
			continue
		}
		checked = append(checked, i)
	}

	ids := make([]string, 0, len(checked))
	for _, i := range checked {
		ids = append(ids, entries[i].QueueEntryID)
	}
	existing, err := lockBatchAdmissions(ctx, tx, ids)
	if err != nil {
		return nil, err
	}

	var pending []pendingEntry
	for _, i := range checked {
		row, exists := existing[entries[i].QueueEntryID]
		switch {
		case exists && !row.lapsedUnclaimed():
			out[i] = replayOutcome(out[i], row)
		case ended:
			// Only a new grant is refused after sales_ends_at (D-C4.4a); a replay above has already returned its own grant.
			// A re-grant of a lapsed row is a new grant of stock, so it is refused here too.
			out[i].Outcome = "SOLD_OUT"
		default:
			p := pendingEntry{entry: i}
			if exists {
				p.regrantID = row.id
			}
			pending = append(pending, p)
		}
	}
	if len(pending) > 0 {
		if err := grantPending(ctx, tx, eventID, entries, pending, out, at); err != nil {
			return nil, err
		}
	}
	for i, e := range entries {
		if j := first[e.QueueEntryID]; j != i {
			out[i] = out[j]
		}
	}
	return out, nil
}

// replayOutcome answers an entry that already holds an admission: its own grant while it is still reserved, otherwise
// ALREADY_ACTIVE, so a released, activated or expired admission is never granted again (D-C6.6).
func replayOutcome(o outbound.ReserveOutcome, row batchRow) outbound.ReserveOutcome {
	o.AllocationID = row.id
	if row.status != "RESERVED" {
		o.Outcome = "ALREADY_ACTIVE"
		return o
	}
	o.Outcome, o.Units = "GRANTED", row.units
	deadline := row.expiresAt
	o.ClaimDeadline = &deadline
	return o
}

// grantPending decides the new grants of a batch against the one locked event_supply row, in entry order. Each decision
// sees the stock the earlier entries of the batch left, and the batch writes the result once (see writeGrants).
func grantPending(ctx context.Context, tx pgx.Tx, eventID string, entries []outbound.ReserveEntry, pending []pendingEntry, out []outbound.ReserveOutcome, at time.Time) error {
	sup, found, err := lockSupply(ctx, tx, eventID)
	if err != nil {
		return err
	}
	alloc := sup.alloc
	free := sup.total - sup.alloc - sup.held - sup.sold
	soldOutAnnounced := false
	// granted counts the units this batch has granted to each buyer NIK hash, so that entries of one buyer share the quota.
	granted := make(map[string]int)
	var grants []batchGrant
	for _, p := range pending {
		e := entries[p.entry]
		o := &out[p.entry]
		if !found {
			o.Outcome = "NO_SUPPLY_NOW"
			continue
		}

		// Buyer quota: only an entry that still carries a NIK hash (legacy; ADR-030 moved the check to hold). The quota left
		// is the stored remainder minus what the earlier entries of this batch took for the same buyer (R27).
		remaining := sup.maxPerOrder
		nik := string(e.BuyerNIKHash)
		if len(e.BuyerNIKHash) > 0 {
			left, ok := buyerRemaining(ctx, tx, eventID, e.BuyerNIKHash)
			if !ok {
				o.Outcome = "NO_SUPPLY_NOW"
				continue
			}
			left -= granted[nik]
			if left <= 0 {
				o.Outcome = "BUYER_QUOTA_EXHAUSTED"
				continue
			}
			remaining = left
		}

		// Sold out only when nothing is free, allocated or held (F4): reserved units can still come back.
		if free+alloc+sup.held == 0 {
			o.Outcome = "SOLD_OUT"
			announceSoldOutSafe(ctx, tx, eventID, &soldOutAnnounced)
			continue
		}
		if free == 0 {
			// free=0 but alloc+held > 0: stock is reserved but not yet held, a temporary unavailability.
			o.Outcome = "NO_SUPPLY_NOW"
			continue
		}

		units := e.Units
		if units > sup.maxPerOrder {
			units = sup.maxPerOrder
		}
		if units > remaining {
			units = remaining
		}
		if units > free {
			units = free
		}
		alloc += units
		free -= units
		if len(e.BuyerNIKHash) > 0 {
			granted[nik] += units
		}
		o.Outcome, o.Units = "GRANTED", units
		grants = append(grants, batchGrant{entry: p.entry, units: units, regrantID: p.regrantID})
	}
	return writeGrants(ctx, tx, eventID, entries, grants, out, at)
}

// lockSupply locks the event_supply row once for the batch and reads its counters. found is false when the event has no
// supply row or is not on sale; every new grant of the batch then waits.
func lockSupply(ctx context.Context, tx pgx.Tx, eventID string) (sup supplyCounters, found bool, err error) {
	err = tx.QueryRow(ctx, `
		SELECT s.total_capacity, s.allocated_units, s.held_units, s.sold_units, ev.max_tickets_per_order
		  FROM booking.event_supply s
		  JOIN booking.events ev ON ev.id = s.event_id AND ev.published_at IS NOT NULL AND ev.status <> 'CANCELLED'
		 WHERE s.event_id=$1::uuid FOR UPDATE OF s`, eventID).
		Scan(&sup.total, &sup.alloc, &sup.held, &sup.sold, &sup.maxPerOrder)
	if errors.Is(err, pgx.ErrNoRows) {
		return sup, false, nil
	}
	if err != nil {
		return sup, false, fmt.Errorf("admissions: lock supply: %w", err)
	}
	return sup, true, nil
}

// buyerRemaining returns the buyer's remaining quota on the event for an entry that carries a NIK hash. It runs in its own
// savepoint: a failure rolls back to it and reports !ok, so that entry gets NO_SUPPLY_NOW and the batch goes on.
func buyerRemaining(ctx context.Context, tx pgx.Tx, eventID string, nikHash []byte) (remaining int, ok bool) {
	if _, err := tx.Exec(ctx, "SAVEPOINT quota"); err != nil {
		return 0, false
	}
	var heldQ, paidQ, maxQ int
	err := func() error {
		if _, err := tx.Exec(ctx, `
			INSERT INTO booking.buyer_quotas (event_id, buyer_nik_hash, max_quantity)
			SELECT $1::uuid, $2, ev.max_tickets_per_buyer_nik
			  FROM booking.events ev WHERE ev.id=$1::uuid
			ON CONFLICT (event_id, buyer_nik_hash) DO NOTHING`, eventID, nikHash); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `
			SELECT held_quantity, paid_quantity, max_quantity
			  FROM booking.buyer_quotas
			 WHERE event_id=$1::uuid AND buyer_nik_hash=$2 FOR UPDATE`, eventID, nikHash).
			Scan(&heldQ, &paidQ, &maxQ)
	}()
	if err != nil {
		_, _ = tx.Exec(ctx, "ROLLBACK TO SAVEPOINT quota")
		return 0, false
	}
	_, _ = tx.Exec(ctx, "RELEASE SAVEPOINT quota")
	return maxQ - (heldQ + paidQ), true
}

// writeGrants writes the granted units, each entry on its own (R24). A lapsed row is re-granted under its own savepoint,
// and the new rows go in one bulk insert that skips conflicts (ON CONFLICT DO NOTHING). An entry whose row was not written
// answers ALREADY_ACTIVE (the old per-entry conflict outcome) and its units are not counted. The supply counter moves once,
// by the units that were written.
func writeGrants(ctx context.Context, tx pgx.Tx, eventID string, entries []outbound.ReserveEntry, grants []batchGrant, out []outbound.ReserveOutcome, at time.Time) error {
	if len(grants) == 0 {
		return nil
	}
	landed := make(map[string]landedRow, len(grants))
	written := 0
	var fresh []batchGrant
	for _, g := range grants {
		e := entries[g.entry]
		if g.regrantID == "" {
			fresh = append(fresh, g)
			continue
		}
		row, err := regrantRow(ctx, tx, g, at)
		switch {
		case err == nil:
			landed[e.QueueEntryID] = row
			written += g.units
		case pkgpg.IsCode(err, pkgpg.CodeUniqueViolation):
			// The visitor or account holds another active admission now: the lapsed row stays as it is.
			out[g.entry] = outbound.ReserveOutcome{QueueEntryID: e.QueueEntryID, Units: e.Units, Outcome: "ALREADY_ACTIVE", AllocationID: g.regrantID}
		default:
			out[g.entry] = outbound.ReserveOutcome{QueueEntryID: e.QueueEntryID, Units: e.Units, Outcome: "NO_SUPPLY_NOW"}
		}
	}

	if len(fresh) > 0 {
		var qids, visitors, jtis, accounts, emails, maskeds []string
		var seqs []int64
		var nikHashes [][]byte
		var units []int
		for _, g := range fresh {
			e := entries[g.entry]
			qids = append(qids, e.QueueEntryID)
			seqs = append(seqs, e.Seq)
			visitors = append(visitors, e.VisitorID)
			nikHashes = append(nikHashes, e.BuyerNIKHash)
			emails = append(emails, e.BuyerEmailEnc)
			maskeds = append(maskeds, e.BuyerNIKMasked)
			units = append(units, g.units)
			jtis = append(jtis, e.AdmissionJTI)
			accounts = append(accounts, e.AccountID)
		}
		rows, err := tx.Query(ctx, `
			INSERT INTO booking.admissions (
				event_id, queue_entry_id, queue_seq, visitor_id, buyer_nik_hash, buyer_email_enc, buyer_nik_masked,
				units, outstanding_units, status, admission_jti, attendee_checks, edit_count,
				cfg_selection_ttl_seconds, cfg_payment_ttl_seconds, cfg_submit_grace_seconds,
				cfg_edit_enabled, cfg_max_edits, cfg_allow_mixed_categories,
				reserved_at, expires_at, account_id)
			SELECT $1::uuid, g.queue_entry_id::uuid, g.seq, g.visitor_id::uuid, g.nik_hash, NULLIF(g.email_enc,''), NULLIF(g.nik_masked,''),
				g.units, g.units, 'RESERVED', g.admission_jti::uuid, 0, 0,
				ev.selection_ttl_seconds, ev.payment_ttl_seconds, ev.selection_submit_grace_seconds,
				ev.selection_edit_enabled, ev.max_selection_edits, ev.allow_mixed_categories,
				$2::timestamptz, $2::timestamptz + make_interval(secs => ev.claim_window_seconds),
				NULLIF(g.account_id,'')::uuid
			  FROM unnest($3::text[], $4::bigint[], $5::text[], $6::bytea[], $7::text[], $8::text[], $9::int[], $10::text[], $11::text[])
				AS g(queue_entry_id, seq, visitor_id, nik_hash, email_enc, nik_masked, units, admission_jti, account_id)
			  CROSS JOIN booking.events ev
			 WHERE ev.id = $1::uuid
			ON CONFLICT DO NOTHING
			RETURNING queue_entry_id::text, id::text, expires_at`,
			eventID, at, qids, seqs, visitors, nikHashes, emails, maskeds, units, jtis, accounts)
		if err != nil {
			return fmt.Errorf("admissions: bulk insert: %w", err)
		}
		inserted := make(map[string]landedRow, len(fresh))
		if err := scanLanded(rows, inserted); err != nil {
			return fmt.Errorf("admissions: bulk insert: %w", err)
		}
		var conflicts []batchGrant
		for _, g := range fresh {
			qid := entries[g.entry].QueueEntryID
			if row, ok := inserted[qid]; ok {
				landed[qid] = row
				written += g.units
				continue
			}
			conflicts = append(conflicts, g)
		}
		if err := answerConflicts(ctx, tx, entries, conflicts, out); err != nil {
			return err
		}
	}

	if written > 0 {
		if _, err := tx.Exec(ctx, `
			UPDATE booking.event_supply
			   SET allocated_units = allocated_units + $2, version = version + 1, updated_at = $3
			 WHERE event_id = $1::uuid`, eventID, written, at); err != nil {
			return fmt.Errorf("admissions: allocate supply: %w", err)
		}
	}
	for _, g := range grants {
		row, ok := landed[entries[g.entry].QueueEntryID]
		if !ok {
			continue // answered above as ALREADY_ACTIVE or NO_SUPPLY_NOW
		}
		o := &out[g.entry]
		o.AllocationID = row.id
		deadline := row.expiresAt
		o.ClaimDeadline = &deadline
	}
	return nil
}

// regrantRow re-grants one lapsed row (D-C6.6) in its own savepoint, so a conflict on that row alone does not abort the batch.
func regrantRow(ctx context.Context, tx pgx.Tx, g batchGrant, at time.Time) (landedRow, error) {
	if _, err := tx.Exec(ctx, "SAVEPOINT regrant"); err != nil {
		return landedRow{}, err
	}
	var row landedRow
	err := tx.QueryRow(ctx, `
		UPDATE booking.admissions a
		   SET status = 'RESERVED', units = $2, outstanding_units = $2,
		       reserved_at = $3::timestamptz,
		       expires_at = $3::timestamptz + make_interval(secs => ev.claim_window_seconds),
		       ended_at = NULL, end_reason = NULL
		  FROM booking.events ev
		 WHERE a.id = $1::uuid AND a.status = 'EXPIRED' AND ev.id = a.event_id
		RETURNING a.id::text, a.expires_at`, g.regrantID, g.units, at).Scan(&row.id, &row.expiresAt)
	if err != nil {
		_, _ = tx.Exec(ctx, "ROLLBACK TO SAVEPOINT regrant")
		return landedRow{}, err
	}
	_, _ = tx.Exec(ctx, "RELEASE SAVEPOINT regrant")
	return row, nil
}

// answerConflicts answers the new grants whose row the bulk insert skipped (R24). When the conflicting row is the same queue
// entry's and it is RESERVED, a concurrent batch has just granted it: the entry gets that grant, as a replay would (R27).
// Any other row, or a visitor or account conflict, is ALREADY_ACTIVE; the allocation id is the same queue entry's admission
// when one exists.
func answerConflicts(ctx context.Context, tx pgx.Tx, entries []outbound.ReserveEntry, conflicts []batchGrant, out []outbound.ReserveOutcome) error {
	if len(conflicts) == 0 {
		return nil
	}
	qids := make([]string, 0, len(conflicts))
	for _, g := range conflicts {
		qids = append(qids, entries[g.entry].QueueEntryID)
	}
	rows, err := tx.Query(ctx, `
		SELECT queue_entry_id::text, id::text, status, units, expires_at
		  FROM booking.admissions WHERE queue_entry_id = ANY($1::uuid[])`, qids)
	if err != nil {
		return fmt.Errorf("admissions: conflict lookup: %w", err)
	}
	defer rows.Close()
	existing := make(map[string]batchRow, len(qids))
	for rows.Next() {
		var qid string
		var row batchRow
		if err := rows.Scan(&qid, &row.id, &row.status, &row.units, &row.expiresAt); err != nil {
			return fmt.Errorf("admissions: conflict lookup: %w", err)
		}
		existing[qid] = row
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("admissions: conflict lookup: %w", err)
	}
	for _, g := range conflicts {
		e := entries[g.entry]
		o := outbound.ReserveOutcome{QueueEntryID: e.QueueEntryID, Units: e.Units, Outcome: "ALREADY_ACTIVE"}
		if row, ok := existing[e.QueueEntryID]; ok {
			o = replayOutcome(o, row)
		}
		out[g.entry] = o
	}
	return nil
}

// scanLanded reads the (queue_entry_id, id, expires_at) rows a batch statement returned, and closes them.
func scanLanded(rows pgx.Rows, landed map[string]landedRow) error {
	defer rows.Close()
	for rows.Next() {
		var qid string
		var row landedRow
		if err := rows.Scan(&qid, &row.id, &row.expiresAt); err != nil {
			return err
		}
		landed[qid] = row
	}
	return rows.Err()
}

// announceSoldOutSafe writes the batch's booking.sold_out row inside a savepoint, so a failed announcement does not abort
// the batch. The row is written at most once per batch (see announceSoldOutTx).
func announceSoldOutSafe(ctx context.Context, tx pgx.Tx, eventID string, announced *bool) {
	if _, err := tx.Exec(ctx, "SAVEPOINT sold_out"); err != nil {
		return
	}
	if err := announceSoldOutTx(ctx, tx, eventID, announced); err != nil {
		_, _ = tx.Exec(ctx, "ROLLBACK TO SAVEPOINT sold_out")
		return
	}
	_, _ = tx.Exec(ctx, "RELEASE SAVEPOINT sold_out")
}

// saleGate reads the event once per batch. It returns the outcome every entry gets before any replay lookup ("" when the
// batch may proceed): a cancelled event answers SOLD_OUT, and an event that is missing, unpublished or not yet open
// answers NO_SUPPLY_NOW. ended reports at >= sales_ends_at; decideBatch applies it to new grants only, after the replay
// lookup (R14). A SOLD_OUT for a cancelled event writes no booking.sold_out row (D-C4.4a).
func saleGate(ctx context.Context, tx pgx.Tx, eventID string, at time.Time) (gate string, ended bool, err error) {
	var status string
	var published bool
	var startsAt, endsAt time.Time
	err = tx.QueryRow(ctx, `
		SELECT status, published_at IS NOT NULL, sales_starts_at, sales_ends_at
		  FROM booking.events WHERE id = $1::uuid`, eventID).
		Scan(&status, &published, &startsAt, &endsAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return "NO_SUPPLY_NOW", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("admissions: read event: %w", err)
	}
	switch {
	case status == "CANCELLED":
		return "SOLD_OUT", false, nil
	case !published, at.Before(startsAt):
		return "NO_SUPPLY_NOW", false, nil
	}
	return "", !at.Before(endsAt), nil
}

// lockBatchAdmissions locks the existing admission rows of the batch's queue entries once, in id order (R18), and returns
// them keyed by queue_entry_id. It runs before any event_supply lock, so the batch follows the admission-then-supply order
// that Release and ExpireSession use. The rows are the replay lookup too: they are locked before they are read.
func lockBatchAdmissions(ctx context.Context, tx pgx.Tx, ids []string) (map[string]batchRow, error) {
	existing := make(map[string]batchRow, len(ids))
	if len(ids) == 0 {
		return existing, nil
	}
	rows, err := tx.Query(ctx, `
		SELECT queue_entry_id::text, id::text, status, COALESCE(end_reason, ''), units, expires_at
		  FROM booking.admissions
		 WHERE queue_entry_id = ANY($1::uuid[]) ORDER BY id FOR UPDATE`, ids)
	if err != nil {
		return nil, fmt.Errorf("admissions: lock batch: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var qid string
		var row batchRow
		if err := rows.Scan(&qid, &row.id, &row.status, &row.endReason, &row.units, &row.expiresAt); err != nil {
			return nil, fmt.Errorf("admissions: lock batch: %w", err)
		}
		existing[qid] = row
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("admissions: lock batch: %w", err)
	}
	return existing, nil
}

// announceSoldOutTx writes the booking.sold_out outbox row. The row is written only while *announced is false, so a
// batch announces at most once; it is set only after the row is written. events.status is not changed: the catalog
// derives SOLD_OUT from supply (AC-C4.2).
func announceSoldOutTx(ctx context.Context, tx pgx.Tx, eventID string, announced *bool) error {
	if *announced {
		return nil
	}
	if err := enqueueOutboxTx(ctx, tx, "booking.sold_out", eventID, map[string]any{"eventId": eventID}); err != nil {
		return err
	}
	*announced = true
	return nil
}

func supplyOutcome(err error) string {
	if pkgpg.IsCode(err, pkgpg.CodeCheckViolation) {
		return "SOLD_OUT"
	}
	return "NO_SUPPLY_NOW"
}

// journeyOutcome maps the end reason chosen by a release to the booking.journey.ended outcome.
func journeyOutcome(reason vo.EndReason) (string, error) {
	switch reason {
	case vo.EndUnclaimed:
		return "UNCLAIMED", nil
	case vo.EndLeftWithoutBuying:
		return "LEFT_WITHOUT_BUYING", nil
	default:
		return "", fmt.Errorf("admissions: release: unsupported end reason %q", reason)
	}
}

// insertJourneyEndedTx writes the booking.journey.ended outbox row inside tx so Queue frees the active slot.
// The payload is exactly {eventId, queueEntryId, outcome}: no NIK, email or token can leak into it.
func insertJourneyEndedTx(ctx context.Context, tx pgx.Tx, admissionID, eventID, queueEntryID, outcome string) error {
	payload, err := json.Marshal(map[string]any{
		"eventId": eventID, "queueEntryId": queueEntryID, "outcome": outcome,
	})
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO booking.outbox_events (routing_key, aggregate_id, schema_version, payload)
		VALUES ('booking.journey.ended', $1::uuid, 1, $2)`, admissionID, payload)
	return err
}

// Release ends a RESERVED/ACTIVE admission that never converted: RELEASED, its outstanding units go back to
// free stock and one booking.journey.ended row is written, all in one transaction under the admission row lock.
// Terminal admissions are a no-op (false, nil). An EDITABLE admission already moved its units into a hold, so it
// is left untouched and ErrBookingLocked is returned (invariants 1 and 2).
func (r *AdmissionRepo) Release(ctx context.Context, allocationID string, reason vo.EndReason) (bool, error) {
	outcome, err := journeyOutcome(reason)
	if err != nil {
		return false, err
	}
	var released bool
	err = pkgpg.WithTx(ctx, r.pool, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		var eventID, queueEntryID string
		var units int
		var status vo.AdmissionStatus
		err := tx.QueryRow(ctx, `SELECT event_id::text, queue_entry_id::text, outstanding_units, status
			FROM booking.admissions WHERE id = $1::uuid FOR UPDATE`, allocationID).
			Scan(&eventID, &queueEntryID, &units, &status)
		if errors.Is(err, pgx.ErrNoRows) {
			return inbound.ErrNotFound
		}
		if err != nil {
			return err
		}
		if !status.IsActive() {
			return nil // already ended; idempotent
		}
		if status == vo.AdmissionEditable {
			return inbound.ErrBookingLocked
		}
		if _, err := tx.Exec(ctx, `
			UPDATE booking.admissions SET status='RELEASED', outstanding_units=0, ended_at=now(), end_reason=$2
			 WHERE id = $1::uuid`, allocationID, string(reason)); err != nil {
			return err
		}
		if units > 0 {
			if _, err := tx.Exec(ctx, `
				UPDATE booking.event_supply SET allocated_units = allocated_units - $2, version = version + 1, updated_at = now()
				 WHERE event_id = $1::uuid`, eventID, units); err != nil {
				return err
			}
		}
		if err := insertJourneyEndedTx(ctx, tx, allocationID, eventID, queueEntryID, outcome); err != nil {
			return err
		}
		released = true
		return nil
	})
	if err != nil {
		return false, err
	}
	return released, nil
}

// DueSessions scans admissions whose expiry lapsed, applying grace for ACTIVE/EDITABLE (design §7.1).
func (r *AdmissionRepo) DueSessions(ctx context.Context, now time.Time, limit int) ([]entity.Admission, error) {
	rows, err := r.pool.Query(ctx, `SELECT `+admissionCols+`
		FROM booking.admissions
		WHERE status IN ('RESERVED','ACTIVE','EDITABLE')
		  AND expires_at + CASE
		        WHEN status = 'RESERVED' THEN interval '0'
		        ELSE (cfg_submit_grace_seconds || ' seconds')::interval
		      END < $1
		ORDER BY expires_at LIMIT $2`, now, limit)
	if err != nil {
		return nil, fmt.Errorf("admissions: due: %w", err)
	}
	defer rows.Close()
	var out []entity.Admission
	for rows.Next() {
		a, err := scanAdmission(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// ExpireSession releases one admission's allocation in its own transaction. Held stock tied to a
// booking is released by the expiry sweeper via the unit of work; here we release the allocation.
func (r *AdmissionRepo) ExpireSession(ctx context.Context, admissionID string, now time.Time) error {
	return pkgpg.WithTx(ctx, r.pool, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		var status vo.AdmissionStatus
		var eventID, queueEntryID string
		var outstanding int
		if err := tx.QueryRow(ctx, `SELECT status, event_id::text, queue_entry_id::text, outstanding_units
			FROM booking.admissions WHERE id=$1::uuid FOR UPDATE`, admissionID).
			Scan(&status, &eventID, &queueEntryID, &outstanding); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil
			}
			return err
		}
		if !status.IsActive() {
			return nil // already terminal, idempotent
		}

		endReason := "SESSION_EXPIRED"
		outcome := "EXPIRED"
		if status == vo.AdmissionReserved {
			endReason = "UNCLAIMED"
			outcome = "UNCLAIMED"
		}

		// Release only this admission's own still-reserved units (outstanding_units): the counter is 0 once
		// the hold consumed/returned them, so a second expiry or an EDITABLE session releases nothing twice.
		if outstanding > 0 {
			if _, err := tx.Exec(ctx, `
				UPDATE booking.event_supply
				   SET allocated_units = allocated_units - $2, version = version + 1, updated_at = $3
				 WHERE event_id = $1::uuid`, eventID, outstanding, now); err != nil {
				return err
			}
		}

		// CONVERTED requires lock_reason (admissions_check2), so it is set in the same statement.
		if status == vo.AdmissionEditable {
			// No journey.ended outbox: the booking is still PENDING_PAYMENT and its expiry sweeper publishes it later.
			_, err := tx.Exec(ctx, `
				UPDATE booking.admissions
				   SET status = 'CONVERTED', outstanding_units = 0, ended_at = $2, end_reason = $3, lock_reason = 'SELECTION_EXPIRED', locked_at = $2
				 WHERE id = $1::uuid`, admissionID, now, endReason)
			return err
		}
		if _, err := tx.Exec(ctx, `
			UPDATE booking.admissions SET status = 'EXPIRED', outstanding_units = 0, ended_at = $2, end_reason = $3 WHERE id = $1::uuid`,
			admissionID, now, endReason); err != nil {
			return err
		}

		// Publish journey.ended so Queue releases the slot (RESERVED/ACTIVE only).
		return insertJourneyEndedTx(ctx, tx, admissionID, eventID, queueEntryID, outcome)
	})
}

func mapAdmissionErr(a entity.Admission, err error) (entity.Admission, error) {
	switch {
	case errors.Is(err, pgx.ErrNoRows), pkgpg.IsCode(err, "22P02"):
		return entity.Admission{}, inbound.ErrNotFound
	case err != nil:
		return entity.Admission{}, fmt.Errorf("admissions: %w", err)
	}
	return a, nil
}

// enqueueOutboxTx writes one outbox row inside an existing transaction (used by announceSoldOutTx).
func enqueueOutboxTx(ctx context.Context, tx pgx.Tx, routingKey, aggregateID string, payload any) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO booking.outbox_events (routing_key, aggregate_id, schema_version, payload)
		VALUES ($1, $2::uuid, 1, $3)`, routingKey, aggregateID, raw)
	return err
}
