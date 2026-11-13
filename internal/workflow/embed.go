package workflow

import "embed"

//go:embed packs/*.yaml
var embeddedPacks embed.FS
