package updatecmd

// targetIdentity describes a capability reported by the selected executable.
type targetIdentity struct {
	offers            bool
	evidence, lacking string
}
