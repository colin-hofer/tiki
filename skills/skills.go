// Package skills embeds the agent skill and CLI instructions.
package skills

import _ "embed"

// Tiki is the installable entry point, which loads instructions from the CLI.
//
//go:embed tiki/SKILL.md
var Tiki string

// Instructions describe the commands and workflows supported by this CLI.
//
//go:embed instructions.md
var Instructions string
