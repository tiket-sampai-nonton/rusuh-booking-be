// Package lua embeds the queue atomicity scripts so they ship inside the binary.
// Lua is used only for atomicity; business logic stays in Go (.kiro/steering/code-principles.md).
package lua

import _ "embed"

// Script sources, embedded at build time.
var (
	//go:embed join.lua
	Join string
	//go:embed promote.lua
	Promote string
	//go:embed claim.lua
	Claim string
	//go:embed leave.lua
	Leave string
	//go:embed end_entry.lua
	EndEntry string
)
