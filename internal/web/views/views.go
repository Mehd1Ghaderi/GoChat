// Package views embeds the web gateway's templates and static assets.
package views

import "embed"

//go:embed templates static
var Files embed.FS
