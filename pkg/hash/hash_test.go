package hash

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPassword(t *testing.T) {
	h, err := Password("correct horse battery staple")
	require.NoError(t, err)
	assert.NotEqual(t, "correct horse battery staple", h)
	assert.True(t, CheckPassword(h, "correct horse battery staple"))
	assert.False(t, CheckPassword(h, "wrong"))
}

func TestRandomTokenAndSHA256(t *testing.T) {
	a, err := RandomToken(32)
	require.NoError(t, err)
	b, err := RandomToken(32)
	require.NoError(t, err)
	assert.NotEqual(t, a, b)
	assert.Len(t, SHA256(a), 64)
	assert.Equal(t, SHA256(a), SHA256(a))
	assert.NotEqual(t, SHA256(a), SHA256(b))
}
