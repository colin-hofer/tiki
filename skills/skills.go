// Package skills embeds the agent instructions distributed by the server.
package skills

import _ "embed"

//go:embed tiki/SKILL.md
var Tiki string
