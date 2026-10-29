package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/yerbolat/emotionsense/internal/domain"
)

// UserRepo — репозиторий пользователей.
type UserRepo struct {
	pool *pgxpool.Pool
}

func NewUserRepo(pool *pgxpool.Pool) *UserRepo { return &UserRepo{pool: pool} }

const userColumns = `id, email, password_hash, role, created_at`

func scanUser(row pgx.Row) (*domain.User, error) {
	var u domain.User
	var role string
	if err := row.Scan(&u.ID, &u.Email, &u.PasswordHash, &role, &u.CreatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	u.Role = domain.Role(role)
	return &u, nil
}

// Create сохраняет пользователя. При дубликате email — ErrConflict.
func (r *UserRepo) Create(ctx context.Context, u *domain.User) error {
	if u.ID == uuid.Nil {
		u.ID = uuid.New()
	}
	if u.CreatedAt.IsZero() {
		u.CreatedAt = time.Now().UTC()
	}
	_, err := r.pool.Exec(ctx, `
		INSERT INTO users (id, email, password_hash, role, created_at)
		VALUES ($1, $2, $3, $4, $5)`,
		u.ID, u.Email, u.PasswordHash, string(u.Role), u.CreatedAt)
	if err != nil {
		if isUniqueViolation(err) {
			return ErrConflict
		}
		return fmt.Errorf("insert user: %w", err)
	}
	return nil
}

func (r *UserRepo) GetByEmail(ctx context.Context, email string) (*domain.User, error) {
	row := r.pool.QueryRow(ctx,
		`SELECT `+userColumns+` FROM users WHERE email = $1`, email)
	return scanUser(row)
}

func (r *UserRepo) GetByID(ctx context.Context, id uuid.UUID) (*domain.User, error) {
	row := r.pool.QueryRow(ctx,
		`SELECT `+userColumns+` FROM users WHERE id = $1`, id)
	return scanUser(row)
}

// List возвращает пользователей постранично (для админки).
func (r *UserRepo) List(ctx context.Context, offset, limit int) ([]domain.User, int64, error) {
	var total int64
	if err := r.pool.QueryRow(ctx, `SELECT count(*) FROM users`).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := r.pool.Query(ctx,
		`SELECT `+userColumns+` FROM users ORDER BY created_at DESC OFFSET $1 LIMIT $2`,
		offset, limit)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var users []domain.User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, 0, err
		}
		users = append(users, *u)
	}
	return users, total, rows.Err()
}

func (r *UserRepo) Count(ctx context.Context) (int64, error) {
	var n int64
	err := r.pool.QueryRow(ctx, `SELECT count(*) FROM users`).Scan(&n)
	return n, err
}

// --- Refresh tokens ---

// SaveRefreshToken сохраняет хэш refresh-токена.
func (r *UserRepo) SaveRefreshToken(ctx context.Context, tokenHash string, userID uuid.UUID, expiresAt time.Time) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO refresh_tokens (token_hash, user_id, expires_at)
		VALUES ($1, $2, $3)`, tokenHash, userID, expiresAt)
	return err
}

// ConsumeRefreshToken удаляет токен и возвращает владельца (ротация токенов).
func (r *UserRepo) ConsumeRefreshToken(ctx context.Context, tokenHash string) (uuid.UUID, error) {
	var userID uuid.UUID
	var expiresAt time.Time
	err := r.pool.QueryRow(ctx, `
		DELETE FROM refresh_tokens WHERE token_hash = $1
		RETURNING user_id, expires_at`, tokenHash).Scan(&userID, &expiresAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, ErrNotFound
		}
		return uuid.Nil, err
	}
	if time.Now().After(expiresAt) {
		return uuid.Nil, ErrNotFound
	}
	return userID, nil
}

// DeleteUserRefreshTokens удаляет все refresh-токены пользователя (logout everywhere).
func (r *UserRepo) DeleteUserRefreshTokens(ctx context.Context, userID uuid.UUID) error {
	_, err := r.pool.Exec(ctx, `DELETE FROM refresh_tokens WHERE user_id = $1`, userID)
	return err
}
