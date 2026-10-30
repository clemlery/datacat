// Package migrations embarque les fichiers SQL versionnés appliqués au démarrage.
package migrations

import "embed"

// FS contient les paires NNNNNN_description.up.sql / .down.sql.
//
//go:embed *.sql
var FS embed.FS
