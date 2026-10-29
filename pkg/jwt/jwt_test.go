package jwt

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestManager_IssueAndParse(t *testing.T) {
	m := NewManager("super-secret-key-1234", time.Minute, "emotionsense")

	tok, err := m.Issue("user-1", "admin")
	require.NoError(t, err)

	claims, err := m.Parse(tok)
	require.NoError(t, err)
	assert.Equal(t, "user-1", claims.UserID)
	assert.Equal(t, "admin", claims.Role)
	assert.Equal(t, "emotionsense", claims.Issuer)
}

func TestManager_RejectsTamperedAndForeign(t *testing.T) {
	m := NewManager("secret-a-secret-a", time.Minute, "iss")
	other := NewManager("secret-b-secret-b", time.Minute, "iss")

	tok, err := m.Issue("u", "user")
	require.NoError(t, err)

	_, err = other.Parse(tok)
	assert.ErrorIs(t, err, ErrInvalidToken, "другой секрет")

	_, err = m.Parse(tok + "x")
	assert.ErrorIs(t, err, ErrInvalidToken, "испорченная подпись")

	_, err = m.Parse("not.a.jwt")
	assert.ErrorIs(t, err, ErrInvalidToken)
}

func TestManager_Expired(t *testing.T) {
	m := NewManager("secret-a-secret-a", -time.Second, "iss")
	tok, err := m.Issue("u", "user")
	require.NoError(t, err)
	_, err = m.Parse(tok)
	assert.ErrorIs(t, err, ErrInvalidToken)
}

func TestManager_WrongIssuer(t *testing.T) {
	a := NewManager("same-secret-same-secret", time.Minute, "issuer-a")
	b := NewManager("same-secret-same-secret", time.Minute, "issuer-b")
	tok, err := a.Issue("u", "user")
	require.NoError(t, err)
	_, err = b.Parse(tok)
	assert.ErrorIs(t, err, ErrInvalidToken)
}
