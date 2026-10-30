package hcclient

import (
	"fmt"
	"path/filepath"
	"regexp"
)

// Endpoint separates transport routing from permanent runtime and ledger identity.
type Endpoint struct {
	Schema            int    `json:"schema"`
	Backend           string `json:"backend"`
	Machine           string `json:"machine"`
	CoreID            string `json:"core_id"`
	Session           string `json:"session"`
	Socket            string `json:"socket"`
	Address           string `json:"address"`
	Task              string `json:"task"`
	Role              string `json:"role"`
	Attempt           string `json:"attempt"`
	BindingGeneration uint64 `json:"binding_generation"`
	CreatedMS         uint64 `json:"created_ms"`
}

var addressPattern = regexp.MustCompile(`^t[0-9a-z]+$`)

func (e Endpoint) Validate() error {
	if e.Schema != 2 || e.Backend != "hc" || e.CoreID == "" || !addressPattern.MatchString(e.Address) || !filepath.IsAbs(e.Socket) || e.Role == "" || e.Attempt == "" || e.BindingGeneration == 0 || e.CreatedMS == 0 {
		return fmt.Errorf("invalid local HC endpoint identity or binding")
	}
	return nil
}
func (e Endpoint) SameTerminal(other Endpoint) bool {
	return e.CoreID != "" && e.CoreID == other.CoreID && e.Address != "" && e.Address == other.Address && e.Machine == other.Machine
}
func (e Endpoint) SameAuthority(other Endpoint) bool {
	return e.Schema == 2 && other.Schema == 2 && e.Backend == "hc" && other.Backend == "hc" && e.SameTerminal(other) && e.Task == other.Task && e.Role == other.Role && e.Attempt != "" && e.Attempt == other.Attempt && e.BindingGeneration != 0 && e.BindingGeneration == other.BindingGeneration && e.CreatedMS != 0 && e.CreatedMS == other.CreatedMS
}
