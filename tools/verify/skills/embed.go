// Package skills holds the verification skills that sum-verify vendors into a project.
package skills

import "embed"

// Vendored names the skill directories copied into a project's .agents/skills.
var Vendored = []string{"verify", "evidence", "maintain-verification"}

//go:embed verify evidence maintain-verification
var Files embed.FS
