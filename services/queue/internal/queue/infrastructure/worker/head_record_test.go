package worker

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// Table over the C8 transition table (design §5.4a.3, rev 8) as a pure function: rows 1-5, N = 3.
func TestHeadRec_NextRec(t *testing.T) {
	const n = 3
	P := "P"
	tests := []struct {
		name    string
		in      *headRec
		obs     tickObs
		want    headRec
		wantEff headEffects
	}{
		{"row1+4: new head, NO_SUPPLY_NOW without adoption", nil, tickObs{headID: P, reached: true, noSupply: true}, headRec{P, 1, 0}, headEffects{}},
		{"row1+3: new head, adoption", nil, tickObs{headID: P, reached: true, noSupply: true, adopted: true}, headRec{P, 1, 1}, headEffects{}},
		{"row1+5: new head, granted", nil, tickObs{headID: P, reached: true}, headRec{P, 0, 0}, headEffects{}},
		{"row2: head did not reach a reply leaves the record untouched", &headRec{P, 2, 2}, tickObs{headID: P, reached: false}, headRec{P, 2, 2}, headEffects{}},
		{"row1+2: new head that did not reach a reply starts at zero", &headRec{"Q", 2, 2}, tickObs{headID: P, reached: false}, headRec{P, 0, 0}, headEffects{}},
		{"row3 below N", &headRec{P, 1, 1}, tickObs{headID: P, reached: true, noSupply: true, adopted: true}, headRec{P, 2, 2}, headEffects{}},
		{"row3 at N: suspect, repeat adopted=true, warn", &headRec{P, 2, 2}, tickObs{headID: P, reached: true, noSupply: true, adopted: true}, headRec{P, 3, 3}, headEffects{Repeat: true, RepeatAdopted: true, Suspect: true, Warn: true}},
		{"row4 at N: repeat adopted=false only", &headRec{P, 2, 0}, tickObs{headID: P, reached: true, noSupply: true}, headRec{P, 3, 0}, headEffects{Repeat: true, RepeatAdopted: false, Warn: true}},
		{"row4 after N: repeat adopted=false, no warn", &headRec{P, 3, 0}, tickObs{headID: P, reached: true, noSupply: true}, headRec{P, 4, 0}, headEffects{Repeat: true}},
		{"alternating: noSupply>=N but adopted<N stays adopted=false", &headRec{P, 3, 1}, tickObs{headID: P, reached: true, noSupply: true, adopted: true}, headRec{P, 4, 2}, headEffects{Repeat: true}},
		{"alternating reaches adopted==N: first adopted=true", &headRec{P, 4, 2}, tickObs{headID: P, reached: true, noSupply: true, adopted: true}, headRec{P, 5, 3}, headEffects{Repeat: true, RepeatAdopted: true, Suspect: true}},
		{"row5 resets both", &headRec{P, 5, 4}, tickObs{headID: P, reached: true}, headRec{P, 0, 0}, headEffects{}},
		{"every 100th tick of a run warns", &headRec{P, 99, 0}, tickObs{headID: P, reached: true, noSupply: true}, headRec{P, 100, 0}, headEffects{Repeat: true, Warn: true}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var before headRec
			if tt.in != nil {
				before = *tt.in
			}
			got, eff := nextRec(tt.in, tt.obs, n)
			assert.Equal(t, tt.want, *got)
			assert.Equal(t, tt.wantEff, eff)
			assert.LessOrEqual(t, got.adoptedTicks, got.noSupplyTicks, "invarian 0 <= adoptedTicks <= noSupplyTicks")
			if tt.in != nil {
				assert.Equal(t, before, *tt.in, "nextRec tidak memutasi record masukan")
			}
		})
	}
}
