package valueobject_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	vo "github.com/tiket-sampai-nonton/rusuh-booking-be/services/payment/internal/payment/domain/valueobject"
)

// Property 4: status final tidak pernah mundur; SUCCEEDED boleh menyusul setelah CANCELLED/EXPIRED.
func TestAttemptStatus_StateMachine(t *testing.T) {
	allowed := map[[2]vo.AttemptStatus]bool{
		{vo.AttemptCreated, vo.AttemptPending}: true, {vo.AttemptCreated, vo.AttemptUnknown}: true,
		{vo.AttemptCreated, vo.AttemptCancelled}: true, {vo.AttemptCreated, vo.AttemptFailed}: true,
		{vo.AttemptUnknown, vo.AttemptPending}: true, {vo.AttemptUnknown, vo.AttemptSucceeded}: true,
		{vo.AttemptUnknown, vo.AttemptFailed}: true, {vo.AttemptUnknown, vo.AttemptCancelled}: true, {vo.AttemptUnknown, vo.AttemptExpired}: true,
		{vo.AttemptPending, vo.AttemptSucceeded}: true, {vo.AttemptPending, vo.AttemptFailed}: true,
		{vo.AttemptPending, vo.AttemptCancelled}: true, {vo.AttemptPending, vo.AttemptExpired}: true,
		{vo.AttemptCancelled, vo.AttemptSucceeded}: true, {vo.AttemptExpired, vo.AttemptSucceeded}: true,
	}
	all := []vo.AttemptStatus{vo.AttemptCreated, vo.AttemptPending, vo.AttemptUnknown, vo.AttemptSucceeded, vo.AttemptFailed, vo.AttemptCancelled, vo.AttemptExpired}
	for _, from := range all {
		for _, to := range all {
			assert.Equal(t, allowed[[2]vo.AttemptStatus{from, to}], from.CanTransitionTo(to), "%s → %s", from, to)
		}
	}
}

func TestAttemptStatus_FinalStatusesNeverMove(t *testing.T) {
	for _, final := range []vo.AttemptStatus{vo.AttemptSucceeded, vo.AttemptFailed} {
		assert.False(t, final.IsOpen())
		for _, to := range []vo.AttemptStatus{vo.AttemptCreated, vo.AttemptPending, vo.AttemptUnknown, vo.AttemptCancelled, vo.AttemptExpired, vo.AttemptSucceeded, vo.AttemptFailed} {
			assert.False(t, final.CanTransitionTo(to), "%s → %s", final, to)
		}
	}
}

func TestAttemptStatus_IsOpenMatchesOpenStatuses(t *testing.T) {
	for _, s := range vo.OpenStatuses {
		assert.True(t, s.IsOpen())
	}
	assert.False(t, vo.AttemptCancelled.IsOpen())
	assert.False(t, vo.AttemptExpired.IsOpen())
}

func TestSourcesOf_ListsEveryStatusThatCanReachTarget(t *testing.T) {
	assert.ElementsMatch(t, []vo.AttemptStatus{vo.AttemptPending, vo.AttemptUnknown, vo.AttemptCancelled, vo.AttemptExpired}, vo.SourcesOf(vo.AttemptSucceeded))
	assert.ElementsMatch(t, []vo.AttemptStatus{vo.AttemptCreated, vo.AttemptUnknown}, vo.SourcesOf(vo.AttemptPending))
	assert.Empty(t, vo.SourcesOf(vo.AttemptCreated), "CREATED hanya status awal")
}
