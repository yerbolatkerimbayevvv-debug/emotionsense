package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yerbolat/emotionsense/internal/apperror"
	"github.com/yerbolat/emotionsense/internal/domain"
	"github.com/yerbolat/emotionsense/internal/dto"
)

func newAuth() (*AuthService, *memUsers) {
	users := newMemUsers()
	return NewAuthService(users, fakeTokens{}, time.Hour), users
}

func TestAuthService_Register(t *testing.T) {
	ctx := context.Background()
	svc, users := newAuth()

	pair, user, err := svc.Register(ctx, dto.RegisterRequest{Email: "  Alice@Example.COM ", Password: "secret123"})
	require.NoError(t, err)
	require.NotNil(t, pair)
	require.NotNil(t, user)

	assert.Equal(t, "alice@example.com", user.Email, "email должен нормализоваться")
	assert.Equal(t, domain.RoleUser, user.Role)
	assert.NotEqual(t, "secret123", user.PasswordHash, "пароль не хранится открытым текстом")
	assert.NotEmpty(t, pair.AccessToken)
	assert.NotEmpty(t, pair.RefreshToken)
	assert.Equal(t, "Bearer", pair.TokenType)
	assert.Equal(t, int64(900), pair.ExpiresIn)
	assert.Len(t, users.refresh, 1, "refresh-токен сохранён (в виде хеша)")

	t.Run("duplicate email -> 409", func(t *testing.T) {
		_, _, err := svc.Register(ctx, dto.RegisterRequest{Email: "alice@example.com", Password: "another1"})
		require.Error(t, err)
		assert.True(t, apperror.Is(err, apperror.CodeConflict))
	})
}

func TestAuthService_Login(t *testing.T) {
	ctx := context.Background()
	svc, _ := newAuth()
	_, _, err := svc.Register(ctx, dto.RegisterRequest{Email: "bob@example.com", Password: "secret123"})
	require.NoError(t, err)

	t.Run("ok", func(t *testing.T) {
		pair, err := svc.Login(ctx, dto.LoginRequest{Email: "BOB@example.com", Password: "secret123"})
		require.NoError(t, err)
		assert.NotEmpty(t, pair.AccessToken)
	})
	t.Run("wrong password", func(t *testing.T) {
		_, err := svc.Login(ctx, dto.LoginRequest{Email: "bob@example.com", Password: "wrong"})
		assert.True(t, apperror.Is(err, apperror.CodeUnauthorized))
	})
	t.Run("unknown user has same error as wrong password", func(t *testing.T) {
		_, err := svc.Login(ctx, dto.LoginRequest{Email: "nobody@example.com", Password: "secret123"})
		assert.True(t, apperror.Is(err, apperror.CodeUnauthorized))
		assert.Equal(t, "invalid email or password", apperror.From(err).Message)
	})
}

func TestAuthService_RefreshRotation(t *testing.T) {
	ctx := context.Background()
	svc, users := newAuth()
	pair, _, err := svc.Register(ctx, dto.RegisterRequest{Email: "c@example.com", Password: "secret123"})
	require.NoError(t, err)

	newPair, err := svc.Refresh(ctx, pair.RefreshToken)
	require.NoError(t, err)
	assert.NotEqual(t, pair.RefreshToken, newPair.RefreshToken, "refresh-токен должен ротироваться")
	assert.Len(t, users.refresh, 1, "старый удалён, новый добавлен")

	t.Run("old token is revoked", func(t *testing.T) {
		_, err := svc.Refresh(ctx, pair.RefreshToken)
		assert.True(t, apperror.Is(err, apperror.CodeUnauthorized))
	})
	t.Run("garbage token", func(t *testing.T) {
		_, err := svc.Refresh(ctx, "garbage")
		assert.True(t, apperror.Is(err, apperror.CodeUnauthorized))
	})
}

func TestAuthService_Logout(t *testing.T) {
	ctx := context.Background()
	svc, users := newAuth()
	pair, user, err := svc.Register(ctx, dto.RegisterRequest{Email: "d@example.com", Password: "secret123"})
	require.NoError(t, err)
	_, err = svc.Login(ctx, dto.LoginRequest{Email: "d@example.com", Password: "secret123"})
	require.NoError(t, err)
	require.Len(t, users.refresh, 2)

	require.NoError(t, svc.Logout(ctx, user.ID))
	assert.Empty(t, users.refresh)

	_, err = svc.Refresh(ctx, pair.RefreshToken)
	assert.True(t, apperror.Is(err, apperror.CodeUnauthorized))
}
