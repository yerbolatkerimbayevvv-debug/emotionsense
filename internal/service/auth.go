package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/yerbolat/emotionsense/internal/apperror"
	"github.com/yerbolat/emotionsense/internal/domain"
	"github.com/yerbolat/emotionsense/internal/dto"
	"github.com/yerbolat/emotionsense/internal/repository/postgres"
	"github.com/yerbolat/emotionsense/pkg/hash"
)

// AuthService — регистрация, вход, обновление и отзыв токенов.
type AuthService struct {
	users      UserRepository
	tokens     TokenIssuer
	refreshTTL time.Duration
}

// NewAuthService создаёт сервис аутентификации.
func NewAuthService(users UserRepository, tokens TokenIssuer, refreshTTL time.Duration) *AuthService {
	return &AuthService{users: users, tokens: tokens, refreshTTL: refreshTTL}
}

// Register создаёт пользователя и сразу выдаёт пару токенов.
func (s *AuthService) Register(ctx context.Context, req dto.RegisterRequest) (*dto.TokenPair, *domain.User, error) {
	email := normalizeEmail(req.Email)

	pwHash, err := hash.Password(req.Password)
	if err != nil {
		return nil, nil, apperror.Internal(fmt.Errorf("hash password: %w", err))
	}

	u := &domain.User{Email: email, PasswordHash: pwHash, Role: domain.RoleUser}
	if err := s.users.Create(ctx, u); err != nil {
		if errors.Is(err, postgres.ErrConflict) {
			return nil, nil, apperror.Conflict("email already registered")
		}
		return nil, nil, apperror.Internal(err)
	}

	pair, err := s.issuePair(ctx, u)
	if err != nil {
		return nil, nil, err
	}
	return pair, u, nil
}

// Login проверяет пароль и выдаёт токены.
func (s *AuthService) Login(ctx context.Context, req dto.LoginRequest) (*dto.TokenPair, error) {
	u, err := s.users.GetByEmail(ctx, normalizeEmail(req.Email))
	if err != nil {
		if errors.Is(err, postgres.ErrNotFound) {
			// Одинаковое сообщение для «нет пользователя» и «неверный пароль»,
			// чтобы не раскрывать существование email.
			return nil, apperror.Unauthorized("invalid email or password")
		}
		return nil, apperror.Internal(err)
	}
	if !hash.CheckPassword(u.PasswordHash, req.Password) {
		return nil, apperror.Unauthorized("invalid email or password")
	}
	return s.issuePair(ctx, u)
}

// Refresh ротирует refresh-токен: старый удаляется, выдаётся новая пара.
func (s *AuthService) Refresh(ctx context.Context, refreshToken string) (*dto.TokenPair, error) {
	userID, err := s.users.ConsumeRefreshToken(ctx, hash.SHA256(refreshToken))
	if err != nil {
		if errors.Is(err, postgres.ErrNotFound) {
			return nil, apperror.Unauthorized("refresh token is invalid or expired")
		}
		return nil, apperror.Internal(err)
	}
	u, err := s.users.GetByID(ctx, userID)
	if err != nil {
		return nil, apperror.Unauthorized("user not found")
	}
	return s.issuePair(ctx, u)
}

// Logout отзывает все refresh-токены пользователя.
func (s *AuthService) Logout(ctx context.Context, userID uuid.UUID) error {
	if err := s.users.DeleteUserRefreshTokens(ctx, userID); err != nil {
		return apperror.Internal(err)
	}
	return nil
}

// Me возвращает профиль текущего пользователя.
func (s *AuthService) Me(ctx context.Context, userID uuid.UUID) (*domain.User, error) {
	u, err := s.users.GetByID(ctx, userID)
	if err != nil {
		if errors.Is(err, postgres.ErrNotFound) {
			return nil, apperror.NotFound("user not found")
		}
		return nil, apperror.Internal(err)
	}
	return u, nil
}

func (s *AuthService) issuePair(ctx context.Context, u *domain.User) (*dto.TokenPair, error) {
	access, err := s.tokens.Issue(u.ID.String(), string(u.Role))
	if err != nil {
		return nil, apperror.Internal(err)
	}
	refresh, err := hash.RandomToken(32)
	if err != nil {
		return nil, apperror.Internal(err)
	}
	expires := time.Now().Add(s.refreshTTL)
	if err := s.users.SaveRefreshToken(ctx, hash.SHA256(refresh), u.ID, expires); err != nil {
		return nil, apperror.Internal(err)
	}
	return &dto.TokenPair{
		AccessToken:  access,
		RefreshToken: refresh,
		ExpiresIn:    int64(s.tokens.TTL().Seconds()),
		TokenType:    "Bearer",
	}, nil
}

func normalizeEmail(e string) string { return strings.ToLower(strings.TrimSpace(e)) }
