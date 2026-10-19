package usecase

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/tiket-sampai-nonton/rusuh-booking-be/pkg/apperror"

	mock_outbound "github.com/tiket-sampai-nonton/rusuh-booking-be/services/user/_mock/user/port/outbound"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/user/internal/user/domain/dto"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/user/internal/user/domain/entity"
	"github.com/tiket-sampai-nonton/rusuh-booking-be/services/user/internal/user/port/inbound"
)

func ptr(s string) *string { return &s }

func newProfile(t *testing.T) (*Profile, *mock_outbound.MockIAccountRepository, *mock_outbound.MockIPIIProtector) {
	ctrl := gomock.NewController(t)
	repo := mock_outbound.NewMockIAccountRepository(ctrl)
	pii := mock_outbound.NewMockIPIIProtector(ctrl)
	return NewProfile(repo, pii), repo, pii
}

func TestGetProfile(t *testing.T) {
	u, repo, _ := newProfile(t)
	repo.EXPECT().GetByID(gomock.Any(), "acc-1").Return(entity.Account{ID: "acc-1", EmailMasked: "a***@b.id"}, nil)
	got, err := u.GetProfile(context.Background(), "acc-1")
	require.NoError(t, err)
	assert.Equal(t, "acc-1", got.AccountID)

	repo.EXPECT().GetByID(gomock.Any(), "gone").Return(entity.Account{}, entity.ErrNotFound)
	_, err = u.GetProfile(context.Background(), "gone")
	assert.Equal(t, inbound.CodeUnauthorized, code(err))
}

func TestUpdateProfile(t *testing.T) {
	ctx := context.Background()
	t.Run("name and nik", func(t *testing.T) {
		u, repo, pii := newProfile(t)
		pii.EXPECT().SealNIK("3174010101900001").Return([]byte("enc"), nil)
		pii.EXPECT().HashNIK("3174010101900001").Return([]byte("nh"))
		repo.EXPECT().UpdateProfile(gomock.Any(), "acc-1", entity.ProfileUpdate{FullName: ptr("Arif Nur"), NIKHash: []byte("nh"), NIKEnc: []byte("enc"), NIKMasked: "3174********0001"}).
			Return(entity.Account{ID: "acc-1", FullName: "Arif Nur", NIKMasked: "3174********0001"}, nil)
		got, err := u.UpdateProfile(ctx, "acc-1", dto.UpdateProfileRequest{FullName: ptr("  Arif Nur "), NIK: ptr("3174010101900001")})
		require.NoError(t, err)
		assert.True(t, got.HasNIK)
	})

	tests := []struct {
		name  string
		req   dto.UpdateProfileRequest
		setup func(*mock_outbound.MockIAccountRepository, *mock_outbound.MockIPIIProtector)
		want  apperror.Code
	}{
		{"nothing", dto.UpdateProfileRequest{}, nil, apperror.CodeValidationFailed},
		{"blank name", dto.UpdateProfileRequest{FullName: ptr("   ")}, nil, apperror.CodeValidationFailed},
		{"bad nik", dto.UpdateProfileRequest{NIK: ptr("123")}, nil, apperror.CodeValidationFailed},
		{"seal error", dto.UpdateProfileRequest{NIK: ptr("3174010101900001")}, func(_ *mock_outbound.MockIAccountRepository, p *mock_outbound.MockIPIIProtector) {
			p.EXPECT().SealNIK(gomock.Any()).Return(nil, errors.New("aes"))
		}, apperror.CodeInternal},
		{"nik taken", dto.UpdateProfileRequest{NIK: ptr("3174010101900001")}, func(r *mock_outbound.MockIAccountRepository, p *mock_outbound.MockIPIIProtector) {
			p.EXPECT().SealNIK(gomock.Any()).Return([]byte("e"), nil)
			p.EXPECT().HashNIK(gomock.Any()).Return([]byte("h"))
			r.EXPECT().UpdateProfile(gomock.Any(), gomock.Any(), gomock.Any()).Return(entity.Account{}, entity.ErrNIKTaken)
		}, inbound.CodeNIKAlreadyLinked},
		{"db error", dto.UpdateProfileRequest{FullName: ptr("A")}, func(r *mock_outbound.MockIAccountRepository, _ *mock_outbound.MockIPIIProtector) {
			r.EXPECT().UpdateProfile(gomock.Any(), gomock.Any(), gomock.Any()).Return(entity.Account{}, errors.New("db"))
		}, apperror.CodeInternal},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			u, repo, pii := newProfile(t)
			if tt.setup != nil {
				tt.setup(repo, pii)
			}
			_, err := u.UpdateProfile(ctx, "acc-1", tt.req)
			assert.Equal(t, tt.want, code(err))
		})
	}
}
