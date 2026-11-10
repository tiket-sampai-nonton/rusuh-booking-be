package entity

import (
	"strconv"

	shared "github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/shared/entity"
)

// SettingsPatch is a partial update: a nil pointer means "not supplied" (keep current, or default on create).
// ActiveSlotCapacity is required at the DTO layer, not here.
type SettingsPatch struct {
	ActiveSlotCapacity          *int
	NearFrontMultiplier         *int
	StatusPublishIntervalMs     *int
	PollFarMs                   *int
	PollMidMs                   *int
	PollNearMs                  *int
	PollFarAbove                *int64
	PollMidAbove                *int64
	OneActivePerVisitor         *bool
	OneActivePerEmail           *bool
	CategoryLimitedThresholdPct *int
	PreTurnNotifyEnabled        *bool
}

// SettingChange is one changed field for the audit trail; Field is the JSON name, Old is nil on create.
type SettingChange struct {
	Field string
	Old   *string
	New   string
}

// SettingsDecider is the single rule function used by the usecase dry run and inside the repository transaction.
type SettingsDecider func(cur EventSettings, exists bool) (EventSettings, error)

type intBound struct {
	field    string
	min, max int
}

type int64Bound struct {
	field    string
	min, max int64
}

var (
	boundCapacity    = intBound{"activeSlotCapacity", 1, 100000}
	boundNearFront   = intBound{"nearFrontMultiplier", 1, 10}
	boundStatus      = intBound{"statusPublishIntervalMs", 250, 5000}
	boundPollFar     = intBound{"pollFarMs", 5000, 60000}
	boundPollMid     = intBound{"pollMidMs", 2000, 30000}
	boundPollNear    = intBound{"pollNearMs", 1000, 5000}
	boundCategory    = intBound{"categoryLimitedThresholdPct", 1, 50}
	boundFarAbove    = int64Bound{"pollFarAbove", 1, 1000000}
	boundMidAbove    = int64Bound{"pollMidAbove", 1, 1000000}
)

func (b intBound) check(v int) *shared.ValidationError {
	if v >= b.min && v <= b.max {
		return nil
	}
	return &shared.ValidationError{
		Field:   b.field,
		Message: "harus antara " + strconv.Itoa(b.min) + " dan " + strconv.Itoa(b.max),
	}
}

func (b int64Bound) check(v int64) *shared.ValidationError {
	if v >= b.min && v <= b.max {
		return nil
	}
	return &shared.ValidationError{
		Field:   b.field,
		Message: "harus antara " + strconv.FormatInt(b.min, 10) + " dan " + strconv.FormatInt(b.max, 10),
	}
}

// DefaultSettings mirrors the column defaults of migration 0001_init and 0005_poll_thresholds (parity is asserted by an integration test).
func DefaultSettings(eventID string) EventSettings {
	return EventSettings{
		EventID:                     eventID,
		ActiveSlotCapacity:          500,
		NearFrontMultiplier:         2,
		StatusPublishIntervalMs:     1000,
		PollFarMs:                   30000,
		PollMidMs:                   10000,
		PollNearMs:                  3000,
		PollFarAbove:                500,
		PollMidAbove:                100,
		OneActivePerVisitor:         true,
		OneActivePerEmail:           true,
		CategoryLimitedThresholdPct: 5,
		PreTurnNotifyEnabled:        true,
	}
}

func pick[T any](p *T, base T) T {
	if p == nil {
		return base
	}
	return *p
}

// Apply merges the supplied fields over base; nil keeps the base value.
func (p SettingsPatch) Apply(base EventSettings) EventSettings {
	base.ActiveSlotCapacity = pick(p.ActiveSlotCapacity, base.ActiveSlotCapacity)
	base.NearFrontMultiplier = pick(p.NearFrontMultiplier, base.NearFrontMultiplier)
	base.StatusPublishIntervalMs = pick(p.StatusPublishIntervalMs, base.StatusPublishIntervalMs)
	base.PollFarMs = pick(p.PollFarMs, base.PollFarMs)
	base.PollMidMs = pick(p.PollMidMs, base.PollMidMs)
	base.PollNearMs = pick(p.PollNearMs, base.PollNearMs)
	base.PollFarAbove = pick(p.PollFarAbove, base.PollFarAbove)
	base.PollMidAbove = pick(p.PollMidAbove, base.PollMidAbove)
	base.OneActivePerVisitor = pick(p.OneActivePerVisitor, base.OneActivePerVisitor)
	base.OneActivePerEmail = pick(p.OneActivePerEmail, base.OneActivePerEmail)
	base.CategoryLimitedThresholdPct = pick(p.CategoryLimitedThresholdPct, base.CategoryLimitedThresholdPct)
	base.PreTurnNotifyEnabled = pick(p.PreTurnNotifyEnabled, base.PreTurnNotifyEnabled)
	return base
}

// ValidateSupplied checks bounds of the supplied fields only (no stored state needed, QSA-2.8).
func (p SettingsPatch) ValidateSupplied() shared.ValidationErrors {
	var errs shared.ValidationErrors
	for _, f := range []struct {
		b intBound
		v *int
	}{
		{boundCapacity, p.ActiveSlotCapacity}, {boundNearFront, p.NearFrontMultiplier},
		{boundStatus, p.StatusPublishIntervalMs}, {boundPollFar, p.PollFarMs}, {boundPollMid, p.PollMidMs},
		{boundPollNear, p.PollNearMs}, {boundCategory, p.CategoryLimitedThresholdPct},
	} {
		if f.v == nil {
			continue
		}
		if e := f.b.check(*f.v); e != nil {
			errs = append(errs, *e)
		}
	}
	for _, f := range []struct {
		b int64Bound
		v *int64
	}{
		{boundFarAbove, p.PollFarAbove}, {boundMidAbove, p.PollMidAbove},
	} {
		if f.v == nil {
			continue
		}
		if e := f.b.check(*f.v); e != nil {
			errs = append(errs, *e)
		}
	}
	return errs
}

// ValidateSettings checks bounds and cross-rules on a complete (merged) value.
func ValidateSettings(s EventSettings) shared.ValidationErrors {
	var errs shared.ValidationErrors
	for _, f := range []struct {
		b intBound
		v int
	}{
		{boundCapacity, s.ActiveSlotCapacity}, {boundNearFront, s.NearFrontMultiplier},
		{boundStatus, s.StatusPublishIntervalMs}, {boundPollFar, s.PollFarMs}, {boundPollMid, s.PollMidMs},
		{boundPollNear, s.PollNearMs}, {boundCategory, s.CategoryLimitedThresholdPct},
	} {
		if e := f.b.check(f.v); e != nil {
			errs = append(errs, *e)
		}
	}
	for _, f := range []struct {
		b int64Bound
		v int64
	}{
		{boundFarAbove, s.PollFarAbove}, {boundMidAbove, s.PollMidAbove},
	} {
		if e := f.b.check(f.v); e != nil {
			errs = append(errs, *e)
		}
	}
	if s.PollNearMs > s.PollMidMs {
		errs = append(errs, shared.ValidationError{Field: "pollNearMs", Message: "tidak boleh lebih besar dari pollMidMs"})
	}
	if s.PollMidMs > s.PollFarMs {
		errs = append(errs, shared.ValidationError{Field: "pollMidMs", Message: "tidak boleh lebih besar dari pollFarMs"})
	}
	if s.StatusPublishIntervalMs > s.PollNearMs {
		errs = append(errs, shared.ValidationError{Field: "statusPublishIntervalMs", Message: "tidak boleh lebih besar dari pollNearMs"})
	}
	if s.PollMidAbove >= s.PollFarAbove {
		errs = append(errs, shared.ValidationError{Field: "pollMidAbove", Message: "tidak boleh lebih besar dari atau sama dengan pollFarAbove"})
	}
	return errs
}

// LockedFieldsChanged reports whether a rule locked while the sale is open differs between old and merged.
func LockedFieldsChanged(old, merged EventSettings) bool {
	return old.OneActivePerVisitor != merged.OneActivePerVisitor || old.OneActivePerEmail != merged.OneActivePerEmail
}

// DiffSettings lists changed fields in a stable order. On create (exists=false) every field is reported with Old=nil.
func DiffSettings(old EventSettings, exists bool, merged EventSettings) []SettingChange {
	type field struct {
		name     string
		old, new string
	}
	fs := []field{
		{"activeSlotCapacity", strconv.Itoa(old.ActiveSlotCapacity), strconv.Itoa(merged.ActiveSlotCapacity)},
		{"nearFrontMultiplier", strconv.Itoa(old.NearFrontMultiplier), strconv.Itoa(merged.NearFrontMultiplier)},
		{"statusPublishIntervalMs", strconv.Itoa(old.StatusPublishIntervalMs), strconv.Itoa(merged.StatusPublishIntervalMs)},
		{"pollFarMs", strconv.Itoa(old.PollFarMs), strconv.Itoa(merged.PollFarMs)},
		{"pollMidMs", strconv.Itoa(old.PollMidMs), strconv.Itoa(merged.PollMidMs)},
		{"pollNearMs", strconv.Itoa(old.PollNearMs), strconv.Itoa(merged.PollNearMs)},
		{"pollFarAbove", strconv.FormatInt(old.PollFarAbove, 10), strconv.FormatInt(merged.PollFarAbove, 10)},
		{"pollMidAbove", strconv.FormatInt(old.PollMidAbove, 10), strconv.FormatInt(merged.PollMidAbove, 10)},
		{"oneActivePerVisitor", strconv.FormatBool(old.OneActivePerVisitor), strconv.FormatBool(merged.OneActivePerVisitor)},
		{"oneActivePerEmail", strconv.FormatBool(old.OneActivePerEmail), strconv.FormatBool(merged.OneActivePerEmail)},
		{"categoryLimitedThresholdPct", strconv.Itoa(old.CategoryLimitedThresholdPct), strconv.Itoa(merged.CategoryLimitedThresholdPct)},
		{"preTurnNotifyEnabled", strconv.FormatBool(old.PreTurnNotifyEnabled), strconv.FormatBool(merged.PreTurnNotifyEnabled)},
	}
	var out []SettingChange
	for _, f := range fs {
		if !exists {
			out = append(out, SettingChange{Field: f.name, New: f.new})
			continue
		}
		if f.old != f.new {
			o := f.old
			out = append(out, SettingChange{Field: f.name, Old: &o, New: f.new})
		}
	}
	return out
}

// Decide is the one rule function: merge over current (or defaults), validate the merged value, then enforce the
// sale-open lock. It is pure: saleOpen comes from the server clock and Booking supply, never from the request.
func Decide(p SettingsPatch, saleOpen bool) SettingsDecider {
	return func(cur EventSettings, exists bool) (EventSettings, error) {
		base := cur
		if !exists {
			base = DefaultSettings(cur.EventID)
		}
		merged := p.Apply(base)
		if errs := ValidateSettings(merged); len(errs) > 0 {
			return EventSettings{}, errs
		}
		if saleOpen && LockedFieldsChanged(base, merged) {
			return EventSettings{}, ErrRulesLocked
		}
		return merged, nil
	}
}
