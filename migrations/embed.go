// Package migrations встраивает SQL-миграции в бинарник,
// чтобы api и worker могли применять их при старте без внешних файлов.
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS
