// Package migrations embeds the versioned SQL of schema users (dijalankan `user migrate up`).
// Aturan: jangan ubah file yang sudah diterapkan; tambah NNNN_nama.up.sql dan .down.sql baru.
package migrations

import "embed"

// FS holds every *.sql file in this directory.
//
//go:embed *.sql
var FS embed.FS
