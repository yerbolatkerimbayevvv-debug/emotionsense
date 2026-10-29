// Package docs встраивает OpenAPI-спецификацию и страницу Swagger UI.
//
// Спецификация написана вручную (spec-first): она является контрактом,
// под который пишется код, а не генерируется из него.
package docs

import _ "embed"

//go:embed openapi.yaml
var OpenAPI []byte

//go:embed swagger.html
var SwaggerHTML []byte
