// Package logger настраивает структурированный логгер на базе log/slog.
//
// В dev — человекочитаемый текст, в stage/prod — JSON, который удобно
// собирать Loki/ELK. Логгер кладётся в context, чтобы хендлеры и сервисы
// могли писать логи с request_id без прокидывания зависимости явно.
package logger

import (
	"context"
	"log/slog"
	"os"
	"strings"
)

type ctxKey struct{}

// New создаёт логгер для указанного уровня и формата.
func New(level string, json bool, attrs ...slog.Attr) *slog.Logger {
	var lvl slog.Level
	switch strings.ToLower(level) {
	case "debug":
		lvl = slog.LevelDebug
	case "warn", "warning":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}

	opts := &slog.HandlerOptions{Level: lvl, AddSource: lvl == slog.LevelDebug}

	var h slog.Handler
	if json {
		h = slog.NewJSONHandler(os.Stdout, opts)
	} else {
		h = slog.NewTextHandler(os.Stdout, opts)
	}
	if len(attrs) > 0 {
		h = h.WithAttrs(attrs)
	}
	l := slog.New(h)
	slog.SetDefault(l)
	return l
}

// WithContext кладёт логгер в контекст.
func WithContext(ctx context.Context, l *slog.Logger) context.Context {
	return context.WithValue(ctx, ctxKey{}, l)
}

// FromContext достаёт логгер из контекста или возвращает дефолтный.
func FromContext(ctx context.Context) *slog.Logger {
	if l, ok := ctx.Value(ctxKey{}).(*slog.Logger); ok && l != nil {
		return l
	}
	return slog.Default()
}
