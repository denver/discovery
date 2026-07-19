// Package migrations embeds the SQL migration files so database mode can
// apply them at startup without shipping loose files. Files follow the
// golang-migrate naming convention: NNNNNN_name.{up,down}.sql, applied in
// filename order by internal/database.
package migrations

import "embed"

// FS contains every *.sql migration file in this directory.
//
//go:embed *.sql
var FS embed.FS
