// Package jwt — выпуск и проверка access-токенов (HS256).
package jwt

import (
	"errors"
	"fmt"
	"time"

	gojwt "github.com/golang-jwt/jwt/v5"
)

// Claims — полезная нагрузка access-токена.
type Claims struct {
	UserID string `json:"uid"`
	Role   string `json:"role"`
	gojwt.RegisteredClaims
}

// Manager подписывает и проверяет токены.
type Manager struct {
	secret []byte
	ttl    time.Duration
	issuer string
}

// ErrInvalidToken — токен не прошёл проверку.
var ErrInvalidToken = errors.New("invalid token")

// NewManager создаёт менеджер.
func NewManager(secret string, ttl time.Duration, issuer string) *Manager {
	return &Manager{secret: []byte(secret), ttl: ttl, issuer: issuer}
}

// TTL возвращает время жизни access-токена.
func (m *Manager) TTL() time.Duration { return m.ttl }

// Issue выпускает подписанный токен.
func (m *Manager) Issue(userID, role string) (string, error) {
	now := time.Now()
	claims := Claims{
		UserID: userID,
		Role:   role,
		RegisteredClaims: gojwt.RegisteredClaims{
			Issuer:    m.issuer,
			Subject:   userID,
			IssuedAt:  gojwt.NewNumericDate(now),
			ExpiresAt: gojwt.NewNumericDate(now.Add(m.ttl)),
		},
	}
	tok := gojwt.NewWithClaims(gojwt.SigningMethodHS256, claims)
	s, err := tok.SignedString(m.secret)
	if err != nil {
		return "", fmt.Errorf("sign token: %w", err)
	}
	return s, nil
}

// Parse проверяет подпись и срок действия, возвращает claims.
func (m *Manager) Parse(tokenStr string) (*Claims, error) {
	claims := &Claims{}
	tok, err := gojwt.ParseWithClaims(tokenStr, claims, func(t *gojwt.Token) (any, error) {
		if _, ok := t.Method.(*gojwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method %v", t.Header["alg"])
		}
		return m.secret, nil
	}, gojwt.WithIssuer(m.issuer), gojwt.WithExpirationRequired())
	if err != nil || !tok.Valid {
		return nil, ErrInvalidToken
	}
	return claims, nil
}
