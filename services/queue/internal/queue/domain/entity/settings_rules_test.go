package entity

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	shared "github.com/tiket-sampai-nonton/rusuh-booking-be/services/queue/internal/shared/entity"
)

func ip(v int) *int   { return &v }
func bp(v bool) *bool { return &v }

func patchCap(v int) SettingsPatch { return SettingsPatch{ActiveSlotCapacity: ip(v)} }

func TestDefaultSettings(t *testing.T) {
	d := DefaultSettings("e1")
	assert.Equal(t, EventSettings{
		EventID: "e1", ActiveSlotCapacity: 500, NearFrontMultiplier: 2, StatusPublishIntervalMs: 1000,
		PollFarMs: 30000, PollMidMs: 10000, PollNearMs: 3000, OneActivePerVisitor: true, OneActivePerEmail: true,
		CategoryLimitedThresholdPct: 5, PreTurnNotifyEnabled: true,
	}, d)
}

func fieldsOf(err error) []string {
	var ve shared.ValidationErrors
	if !errors.As(err, &ve) {
		return nil
	}
	out := []string{}
	for _, e := range ve {
		out = append(out, e.Field)
	}
	return out
}

func TestValidateSupplied_Bounds(t *testing.T) {
	cases := []struct {
		field    string
		min, max int
		set      func(*SettingsPatch, int)
	}{
		{"activeSlotCapacity", 1, 100000, func(p *SettingsPatch, v int) { p.ActiveSlotCapacity = ip(v) }},
		{"nearFrontMultiplier", 1, 10, func(p *SettingsPatch, v int) { p.NearFrontMultiplier = ip(v) }},
		{"statusPublishIntervalMs", 250, 5000, func(p *SettingsPatch, v int) { p.StatusPublishIntervalMs = ip(v) }},
		{"pollFarMs", 5000, 60000, func(p *SettingsPatch, v int) { p.PollFarMs = ip(v) }},
		{"pollMidMs", 2000, 30000, func(p *SettingsPatch, v int) { p.PollMidMs = ip(v) }},
		{"pollNearMs", 1000, 5000, func(p *SettingsPatch, v int) { p.PollNearMs = ip(v) }},
		{"categoryLimitedThresholdPct", 1, 50, func(p *SettingsPatch, v int) { p.CategoryLimitedThresholdPct = ip(v) }},
	}
	for _, c := range cases {
		for _, tc := range []struct {
			name string
			v    int
			ok   bool
		}{{"min-1", c.min - 1, false}, {"min", c.min, true}, {"max", c.max, true}, {"max+1", c.max + 1, false}} {
			t.Run(c.field+"/"+tc.name, func(t *testing.T) {
				var p SettingsPatch
				c.set(&p, tc.v)
				errs := p.ValidateSupplied()
				if tc.ok {
					assert.Empty(t, errs)
					return
				}
				require.Len(t, errs, 1)
				assert.Equal(t, c.field, errs[0].Field)
			})
		}
	}
	assert.Empty(t, SettingsPatch{}.ValidateSupplied())
}

func TestValidateSettings_CrossRules(t *testing.T) {
	base := DefaultSettings("e")
	mod := func(f func(*EventSettings)) EventSettings { s := base; f(&s); return s }
	cases := []struct {
		name string
		s    EventSettings
		bad  string
	}{
		{"defaults ok", base, ""},
		{"near==mid ok", mod(func(s *EventSettings) { s.PollNearMs = 4000; s.PollMidMs = 4000 }), ""},
		{"near>mid", mod(func(s *EventSettings) { s.PollNearMs = 4001; s.PollMidMs = 4000 }), "pollNearMs"},
		{"mid==far ok", mod(func(s *EventSettings) { s.PollMidMs = 20000; s.PollFarMs = 20000 }), ""},
		{"mid>far", mod(func(s *EventSettings) { s.PollMidMs = 20001; s.PollFarMs = 20000 }), "pollMidMs"},
		{"status==near ok", mod(func(s *EventSettings) { s.StatusPublishIntervalMs = 3000 }), ""},
		{"status>near", mod(func(s *EventSettings) { s.StatusPublishIntervalMs = 3001 }), "statusPublishIntervalMs"},
		{"out of bounds", mod(func(s *EventSettings) { s.ActiveSlotCapacity = 0 }), "activeSlotCapacity"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			errs := ValidateSettings(c.s)
			if c.bad == "" {
				assert.Empty(t, errs)
				return
			}
			require.NotEmpty(t, errs)
			assert.Equal(t, c.bad, errs[0].Field)
		})
	}
}

func TestDecide_MergedCrossRuleWithOmittedField(t *testing.T) {
	cur := DefaultSettings("e")
	cur.PollMidMs = 2000
	p := SettingsPatch{ActiveSlotCapacity: ip(10), PollNearMs: ip(3000)} // near 3000 > stored mid 2000
	_, err := Decide(p, false)(cur, true)
	assert.Equal(t, []string{"pollNearMs"}, fieldsOf(err))
}

func TestDecide_LockMatrix(t *testing.T) {
	def := DefaultSettings("e")
	locked := def
	locked.OneActivePerVisitor = false
	cases := []struct {
		name     string
		patch    SettingsPatch
		cur      EventSettings
		exists   bool
		saleOpen bool
		wantErr  error
	}{
		{"closed change locked", SettingsPatch{ActiveSlotCapacity: ip(1), OneActivePerVisitor: bp(false)}, def, true, false, nil},
		{"open change visitor", SettingsPatch{ActiveSlotCapacity: ip(1), OneActivePerVisitor: bp(false)}, def, true, true, ErrRulesLocked},
		{"open change email", SettingsPatch{ActiveSlotCapacity: ip(1), OneActivePerEmail: bp(false)}, def, true, true, ErrRulesLocked},
		{"open same locked value", SettingsPatch{ActiveSlotCapacity: ip(1), OneActivePerVisitor: bp(true)}, def, true, true, nil},
		{"open omitted locked", patchCap(7), locked, true, true, nil},
		{"open non-locked change", SettingsPatch{ActiveSlotCapacity: ip(9), PollNearMs: ip(2000)}, def, true, true, nil},
		{"create open default locked", patchCap(5), EventSettings{}, false, true, nil},
		{"create open non-default locked", SettingsPatch{ActiveSlotCapacity: ip(5), OneActivePerEmail: bp(false)}, EventSettings{}, false, true, ErrRulesLocked},
		{"create closed non-default locked", SettingsPatch{ActiveSlotCapacity: ip(5), OneActivePerEmail: bp(false)}, EventSettings{}, false, false, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := Decide(c.patch, c.saleOpen)(c.cur, c.exists)
			if c.wantErr != nil {
				assert.ErrorIs(t, err, c.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, *c.patch.ActiveSlotCapacity, got.ActiveSlotCapacity)
		})
	}
}

func TestDecide_ValidationBeforeLock(t *testing.T) {
	cur := DefaultSettings("e")
	p := SettingsPatch{ActiveSlotCapacity: ip(0), OneActivePerVisitor: bp(false)}
	_, err := Decide(p, true)(cur, true)
	assert.NotErrorIs(t, err, ErrRulesLocked)
	assert.Equal(t, []string{"activeSlotCapacity"}, fieldsOf(err))
}

func TestDecide_KeepsEventIDAndUpdatedAt(t *testing.T) {
	cur := DefaultSettings("e1")
	cur.UpdatedAt = time.Unix(100, 0)
	got, err := Decide(patchCap(9), false)(cur, true)
	require.NoError(t, err)
	assert.Equal(t, "e1", got.EventID)
}

func TestDiffSettings(t *testing.T) {
	cur := DefaultSettings("e")
	t.Run("no-op", func(t *testing.T) {
		assert.Empty(t, DiffSettings(cur, true, cur))
	})
	t.Run("update changed only, stable order", func(t *testing.T) {
		m := cur
		m.PollFarMs = 40000
		m.ActiveSlotCapacity = 10
		m.PreTurnNotifyEnabled = false
		got := DiffSettings(cur, true, m)
		s, f, tr := "500", "30000", "true"
		assert.Equal(t, []SettingChange{
			{Field: "activeSlotCapacity", Old: &s, New: "10"},
			{Field: "pollFarMs", Old: &f, New: "40000"},
			{Field: "preTurnNotifyEnabled", Old: &tr, New: "false"},
		}, got)
	})
	t.Run("create emits all 10 with nil old", func(t *testing.T) {
		got := DiffSettings(EventSettings{}, false, cur)
		require.Len(t, got, 10)
		for _, c := range got {
			assert.Nil(t, c.Old, c.Field)
		}
		assert.Equal(t, "activeSlotCapacity", got[0].Field)
		assert.Equal(t, "preTurnNotifyEnabled", got[9].Field)
	})
}
