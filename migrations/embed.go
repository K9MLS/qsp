// Package migrations embeds QSP's ordered SQL schema migrations.
//
// The SQL files live here, at the repository root, where they are easy to find
// and review. They are embedded into the binary rather than read from disk so
// that a deployed instance cannot drift from the schema its code expects.
//
// This package exists solely to provide the embedded filesystem; the loading,
// validation and application logic belongs to internal/database.
package migrations

import "embed"

//go:embed *.sql
var files embed.FS

// FS returns the embedded migration files.
//
// Every entry is named NNNN_description.sql, where NNNN is a four-digit,
// one-based, contiguous version number.
func FS() embed.FS { return files }
