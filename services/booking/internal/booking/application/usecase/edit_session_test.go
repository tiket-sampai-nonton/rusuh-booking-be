package usecase_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/application/usecase"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/dto"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/entity"
	vo "github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/domain/valueobject"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/inbound"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/booking/internal/booking/port/outbound"
)

const bookingID = "66666666-6666-6666-6666-666666666666"

func editUC(bk entity.Booking, adm entity.Admission, uow *fakeUoW) *usecase.Edit {
	return usecase.NewEdit(
		&fakeBookingRepo{booking: bk},
		&fakeAdmissionRepo{admission: adm},
		&fakeEventRepo{detail: detailWith(true, 3)},
		uow, fakeNIK{}, fixedClock{t: time.Now()},
	)
}

func editCmd(version int) inbound.EditCommand {
	return inbound.EditCommand{
		BookingID: bookingID,
		VisitorID: visitor,
		Request: dto.ReplaceItemsRequest{Version: version, Items: []dto.HoldItem{
			{CategoryID: catA, Attendee: dto.AttendeeInput{NIK: nik1, FullName: "Alice"}},
		}},
	}
}

func TestEdit_VersionConflict(t *testing.T) {
	bk := entity.Booking{ID: bookingID, EventID: eventID, AdmissionID: sessionID, VisitorID: visitor, Status: vo.BookingPendingPayment, Version: 2}
	uc := editUC(bk, activeAdmission(true), &fakeUoW{})
	_, err := uc.ReplaceItems(context.Background(), editCmd(1))
	assert.ErrorIs(t, err, inbound.ErrVersionConflict)
}

func TestEdit_Locked(t *testing.T) {
	started := time.Now()
	bk := entity.Booking{ID: bookingID, EventID: eventID, AdmissionID: sessionID, VisitorID: visitor, Status: vo.BookingPendingPayment, Version: 1, PaymentStartedAt: &started}
	uc := editUC(bk, activeAdmission(true), &fakeUoW{})
	_, err := uc.ReplaceItems(context.Background(), editCmd(1))
	assert.ErrorIs(t, err, inbound.ErrBookingLocked)
}

func TestEdit_EditLimit(t *testing.T) {
	bk := entity.Booking{ID: bookingID, EventID: eventID, AdmissionID: sessionID, VisitorID: visitor, Status: vo.BookingPendingPayment, Version: 1}
	adm := activeAdmission(true)
	adm.EditCount = 3 // == CfgMaxEdits
	uc := editUC(bk, adm, &fakeUoW{})
	_, err := uc.ReplaceItems(context.Background(), editCmd(1))
	assert.ErrorIs(t, err, inbound.ErrEditLimit)
}

func TestEdit_Success(t *testing.T) {
	bk := entity.Booking{ID: bookingID, EventID: eventID, AdmissionID: sessionID, VisitorID: visitor, Status: vo.BookingPendingPayment, Version: 1}
	uow := &fakeUoW{replaceVer: 2}
	uc := usecase.NewEdit(
		&fakeBookingRepo{booking: bk},
		&fakeAdmissionRepo{admission: activeAdmission(true)},
		&fakeEventRepo{detail: detailWith(true, 3)},
		uow, fakeNIK{}, fixedClock{t: time.Now()},
	)
	resp, err := uc.ReplaceItems(context.Background(), editCmd(1))
	require.NoError(t, err)
	assert.Equal(t, 2, resp.Version)
}

func TestSession_CreateTokenInvalid(t *testing.T) {
	uc := usecase.NewSession(&fakeAdmissionRepo{}, &fakeEventRepo{}, &fakeTokenVerifier{err: assertErr{}}, fixedClock{t: time.Now()})
	_, err := uc.CreateSession(context.Background(), dto.CreateSessionRequest{AdmissionToken: "x"}, visitor)
	assert.ErrorIs(t, err, inbound.ErrAdmissionToken)
}

type assertErr struct{}

func (assertErr) Error() string { return "invalid" }

func TestSession_GetSession_OK(t *testing.T) {
	adm := activeAdmission(true)
	adm.ExpiresAt = time.Now().Add(time.Hour)
	uc := usecase.NewSession(&fakeAdmissionRepo{admission: adm}, &fakeEventRepo{detail: detailWith(true, 3)}, &fakeTokenVerifier{}, fixedClock{t: time.Now()})
	sess, err := uc.GetSession(context.Background(), sessionID, visitor)
	require.NoError(t, err)
	assert.Equal(t, sessionID, sess.SessionID)
}

func TestSession_GetSession_InvalidID(t *testing.T) {
	uc := usecase.NewSession(&fakeAdmissionRepo{}, &fakeEventRepo{}, &fakeTokenVerifier{}, fixedClock{t: time.Now()})
	_, err := uc.GetSession(context.Background(), "not-uuid", visitor)
	assert.Error(t, err)
}

func TestSession_GetSession_Expired(t *testing.T) {
	adm := activeAdmission(true)
	adm.ExpiresAt = time.Now().Add(-time.Minute)
	uc := usecase.NewSession(&fakeAdmissionRepo{admission: adm}, &fakeEventRepo{detail: detailWith(true, 3)}, &fakeTokenVerifier{}, fixedClock{t: time.Now()})
	_, err := uc.GetSession(context.Background(), sessionID, visitor)
	assert.ErrorIs(t, err, inbound.ErrSessionExpired)
}

func TestSession_GetCurrentSession_InvalidEventID(t *testing.T) {
	uc := usecase.NewSession(&fakeAdmissionRepo{}, &fakeEventRepo{}, &fakeTokenVerifier{}, fixedClock{t: time.Now()})
	_, err := uc.GetCurrentSession(context.Background(), "bad-id", visitor)
	assert.Error(t, err)
}

func TestSession_GetCurrentSession_NotFound(t *testing.T) {
	uc := usecase.NewSession(&fakeAdmissionRepo{getErr: inbound.ErrNotFound}, &fakeEventRepo{}, &fakeTokenVerifier{}, fixedClock{t: time.Now()})
	_, err := uc.GetCurrentSession(context.Background(), eventID, visitor)
	assert.ErrorIs(t, err, inbound.ErrNotFound)
}

func TestGetTickets_Success(t *testing.T) {
	bk := entity.Booking{ID: sessionID, Status: "PAID", VisitorID: visitor}
	tickets := []entity.Ticket{
		{ID: "t1", BookingID: sessionID, PublicCode: "TICKET-001"},
	}
	uc := usecase.NewGetTickets(&fakeBookingRepo{booking: bk}, &fakeTicketRepo{tickets: tickets}, nil)
	result, err := uc.GetTickets(context.Background(), sessionID, inbound.TicketAccess{VisitorID: visitor})
	require.NoError(t, err)
	assert.Equal(t, sessionID, result.BookingID)
	assert.Len(t, result.Tickets, 1)
}

func TestGetTickets_NotPaid(t *testing.T) {
	bk := entity.Booking{ID: sessionID, Status: "PENDING_PAYMENT", VisitorID: visitor}
	uc := usecase.NewGetTickets(&fakeBookingRepo{booking: bk}, &fakeTicketRepo{}, nil)
	_, err := uc.GetTickets(context.Background(), sessionID, inbound.TicketAccess{VisitorID: visitor})
	assert.ErrorIs(t, err, inbound.ErrNotPayable)
}

type linkCheck struct{ ok string }

func (l linkCheck) Valid(_, tok string) bool { return tok == l.ok }

// A booking id alone never opens the tickets: only the booking's visitor, its buyer account, or the email link do.
func TestGetTickets_OnlyTheOwnerOrTheEmailLinkCanRead(t *testing.T) {
	bk := entity.Booking{ID: sessionID, Status: "PAID", VisitorID: visitor, AccountID: "acc-1"}
	tickets := []entity.Ticket{{ID: "t1", BookingID: sessionID, PublicCode: "TK-1"}}
	uc := usecase.NewGetTickets(&fakeBookingRepo{booking: bk}, &fakeTicketRepo{tickets: tickets}, linkCheck{ok: "good-link"})
	ctx := context.Background()

	for name, a := range map[string]inbound.TicketAccess{
		"pemilik (visitor)":     {VisitorID: visitor},
		"akun pembeli":          {AccountID: "acc-1"},
		"tautan dari email":     {LinkToken: "good-link"},
		"visitor salah, tautan": {VisitorID: "lain", LinkToken: "good-link"},
	} {
		out, err := uc.GetTickets(ctx, sessionID, a)
		require.NoError(t, err, name)
		assert.Len(t, out.Tickets, 1, name)
	}
	for name, a := range map[string]inbound.TicketAccess{
		"tanpa identitas":  {},
		"visitor lain":     {VisitorID: "lain"},
		"akun lain":        {AccountID: "acc-2"},
		"tautan palsu":     {LinkToken: "bad-link"},
		"akun kosong sama": {AccountID: ""},
		"visitor kosong":   {VisitorID: ""},
		"tautan kosong":    {LinkToken: ""},
	} {
		_, err := uc.GetTickets(ctx, sessionID, a)
		assert.ErrorIs(t, err, inbound.ErrNotFound, name+": tidak boleh membedakan 'tidak ada' dan 'bukan milik Anda'")
	}

	guestBooking := entity.Booking{ID: sessionID, Status: "PAID"} // no account: an empty account id must not match
	g := usecase.NewGetTickets(&fakeBookingRepo{booking: guestBooking}, &fakeTicketRepo{tickets: tickets}, nil)
	_, err := g.GetTickets(ctx, sessionID, inbound.TicketAccess{})
	assert.ErrorIs(t, err, inbound.ErrNotFound)
}

func TestSession_CreateRequiresATokenAndMatchesTheVisitor(t *testing.T) {
	ctx := context.Background()
	clk := fixedClock{t: time.Now()}

	uc := usecase.NewSession(&fakeAdmissionRepo{}, &fakeEventRepo{}, &fakeTokenVerifier{}, clk)
	_, err := uc.CreateSession(ctx, dto.CreateSessionRequest{}, visitor)
	assert.Error(t, err, "admissionToken kosong")

	// The admission token names a visitor: a different caller cannot open the session.
	mismatch := usecase.NewSession(&fakeAdmissionRepo{}, &fakeEventRepo{}, &fakeTokenVerifier{claims: outbound.AdmissionClaims{JTI: "j1", VisitorID: "orang-lain"}}, clk)
	_, err = mismatch.CreateSession(ctx, dto.CreateSessionRequest{AdmissionToken: "tok"}, visitor)
	assert.ErrorIs(t, err, inbound.ErrVisitorMismatch)

	// A token that was already used (or expired) is refused by the repository.
	used := usecase.NewSession(&fakeAdmissionRepo{activErr: inbound.ErrAdmissionToken}, &fakeEventRepo{}, &fakeTokenVerifier{claims: outbound.AdmissionClaims{JTI: "j1"}}, clk)
	_, err = used.CreateSession(ctx, dto.CreateSessionRequest{AdmissionToken: "tok"}, visitor)
	assert.ErrorIs(t, err, inbound.ErrAdmissionToken)

	adm := activeAdmission(true)
	adm.ExpiresAt = time.Now().Add(time.Hour)
	ok := usecase.NewSession(&fakeAdmissionRepo{activated: adm}, &fakeEventRepo{detail: detailWith(true, 3)}, &fakeTokenVerifier{claims: outbound.AdmissionClaims{JTI: "j1", VisitorID: visitor}}, clk)
	sess, err := ok.CreateSession(ctx, dto.CreateSessionRequest{AdmissionToken: "tok"}, visitor)
	require.NoError(t, err)
	assert.Equal(t, sessionID, sess.SessionID)
}
