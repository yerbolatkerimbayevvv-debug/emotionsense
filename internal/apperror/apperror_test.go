package apperror

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestFromAndIs(t *testing.T) {
	base := NotFound("user not found")
	wrapped := fmt.Errorf("service: %w", base)

	assert.True(t, Is(wrapped, CodeNotFound))
	assert.False(t, Is(wrapped, CodeConflict))
	assert.Equal(t, base, From(wrapped))

	plain := errors.New("boom")
	got := From(plain)
	assert.Equal(t, CodeInternal, got.Code)
	assert.ErrorIs(t, got, plain, "причина сохраняется в цепочке")
}

func TestDetailsAndMessage(t *testing.T) {
	e := InvalidInput("validation failed").WithDetails(map[string]any{"email": "required"})
	assert.Equal(t, "required", e.Details["email"])
	assert.Equal(t, "INVALID_INPUT: validation failed", e.Error())

	w := Wrap(CodeUnavailable, "db down", errors.New("dial tcp"))
	assert.Contains(t, w.Error(), "dial tcp")
}
