// Package config holds static assets compiled into the bot.
package config

import "embed"

// Fonts are the open-licence (SIL OFL) preset fonts used by the banner renderer; licences sit alongside.
//
//go:embed fonts/*.ttf
var Fonts embed.FS
