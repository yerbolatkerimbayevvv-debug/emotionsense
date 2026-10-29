// Package apperror определяет типизированные ошибки приложения.
//
// Сервисный слой возвращает *Error с кодом, а HTTP-слой централизованно
// переводит код в статус ответа. Благодаря этому хендлеры не содержат
// if err == ... цепочек, а бизнес-логика не знает про HTTP.
package apperror

import (
	"errors"
	"fmt"
)

// Code — машиночитаемый код ошибки.
type Code string

const (
	CodeInvalidInput Code = "INVALID_INPUT"
	CodeUnauthorized Code = "UNAUTHORIZED"
	CodeForbidden    Code = "FORBIDDEN"
	CodeNotFound     Code = "NOT_FOUND"
	CodeConflict     Code = "CONFLICT"
	CodeTooManyReq   Code = "TOO_MANY_REQUESTS"
	CodeUnavailable  Code = "UNAVAILABLE"
	CodeInternal     Code = "INTERNAL"
)

// Error — ошибка приложения с кодом и опциональной причиной.
type Error struct {
	Code    Code
	Message string
	Err     error          // внутренняя причина (не отдаётся клиенту)
	Details map[string]any // дополнительные поля для клиента (например, ошибки валидации)
}

func (e *Error) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%s: %s: %v", e.Code, e.Message, e.Err)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

// Unwrap позволяет errors.Is / errors.As доходить до причины.
func (e *Error) Unwrap() error { return e.Err }

// WithDetails добавляет детали для клиента.
func (e *Error) WithDetails(d map[string]any) *Error {
	e.Details = d
	return e
}

// New создаёт ошибку с кодом и сообщением.
func New(code Code, msg string) *Error { return &Error{Code: code, Message: msg} }

// Wrap оборачивает причину.
func Wrap(code Code, msg string, err error) *Error {
	return &Error{Code: code, Message: msg, Err: err}
}

// Конструкторы для частых случаев.

func InvalidInput(msg string) *Error { return New(CodeInvalidInput, msg) }
func Unauthorized(msg string) *Error { return New(CodeUnauthorized, msg) }
func Forbidden(msg string) *Error    { return New(CodeForbidden, msg) }
func NotFound(msg string) *Error     { return New(CodeNotFound, msg) }
func Conflict(msg string) *Error     { return New(CodeConflict, msg) }
func TooMany(msg string) *Error      { return New(CodeTooManyReq, msg) }
func Unavailable(msg string, err error) *Error {
	return Wrap(CodeUnavailable, msg, err)
}
func Internal(err error) *Error { return Wrap(CodeInternal, "internal error", err) }

// From извлекает *Error из цепочки; если его нет — считает ошибку внутренней.
func From(err error) *Error {
	var e *Error
	if errors.As(err, &e) {
		return e
	}
	return Internal(err)
}

// Is — удобная проверка кода.
func Is(err error, code Code) bool {
	var e *Error
	return errors.As(err, &e) && e.Code == code
}
