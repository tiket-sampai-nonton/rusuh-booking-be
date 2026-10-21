package ids_test

import (
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/infrastructure/ids"
)

func TestGen_NewIDUnique(t *testing.T) {
	g := ids.New()
	a, b := g.NewID(), g.NewID()
	assert.NotEqual(t, a, b)
	assert.Regexp(t, regexp.MustCompile(`^[0-9a-f-]{36}$`), a)
}

func TestGen_PublicRefShape(t *testing.T) {
	g := ids.New()
	assert.Regexp(t, regexp.MustCompile(`^RB-[0-9A-F]{8}$`), g.NewPublicRef())
}
