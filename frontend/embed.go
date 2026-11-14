// Package frontend supplies the compiled application's static website.
package frontend

import "embed"

// Assets includes only public site files; server data and configuration are never embedded.
//
//go:embed static
var Assets embed.FS
