// Package migrations exposes Prompt Better's immutable SQL migration files.
package migrations

import "embed"

// Files contains ordered runtime migration SQL.
//
//go:embed *.sql
var Files embed.FS

// AdminFiles contains explicit administrator-only bootstrap SQL.
//
//go:embed admin/*.sql
var AdminFiles embed.FS
