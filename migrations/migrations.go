// Package migrations embeds the ordered SQL migrations in the application.
package migrations

import "embed"

//go:embed *.sql
var Files embed.FS
