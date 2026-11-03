package cli

import (
	"os"
	"strings"
)

// testClaimArgs carries only the token captured by an explicit coordinator fixture.
// Task callers and deliberately unqualified callers never inherit that authority.
func testClaimArgs(home string, args []string) []string {
	prefix := []string{"--home", home}
	role := os.Getenv("SUM_ROLE")
	explicit := false
	for _, arg := range args {
		if arg == "--claim" || strings.HasPrefix(arg, "--claim=") {
			explicit = true
		}
	}
	if !explicit && os.Getenv("SUM_TASK") == "" && (role == "" || role == "coordinator") {
		if value, ok := nativeLabs.Load(home); ok {
			prefix = append(prefix, "--claim", value.(*demoLab).claim)
		}
	}
	return append(prefix, args...)
}
