package contract

const (
	SumVersion  = "0.1.0"
	StateSchema = 1
	BriefSchema = 1
	// MachineIdentity 1 says this binary resolves recorded machine values
	// through internal/machine. Only release-contract reports it: a checkout
	// target runs its prebuilt .local/bin/sumctl, so the binary must say what
	// it is. Staging never copies it into release.json, where it would describe
	// the stager rather than the staged tree.
	MachineIdentity = 1
	// WakeProtocol 1 says this binary keeps the coordinator wake sidecar
	// (internal/returns/wake.go): at most one outstanding routine wake per
	// adopted coordinator, persisted before any prompt. Reported the same way
	// as MachineIdentity, by release-contract only.
	WakeProtocol = 1
	HerdrCLI     = "0.9.0"
)

var MCP = MCPContract{Server: "herdr-mesh-sum", Version: SumVersion, Tools: 10}

type MCPContract struct {
	Server  string `json:"server"`
	Version string `json:"version"`
	Tools   int    `json:"tools"`
}

type Contracts struct {
	HerdrCLI string      `json:"herdr_cli"`
	MCP      MCPContract `json:"mcp"`
}

type Supports struct {
	StateSchema     []int `json:"state_schema"`
	BriefSchema     []int `json:"brief_schema"`
	MachineIdentity []int `json:"machine_identity"`
	WakeProtocol    []int `json:"wake_protocol"`
}

type Release struct {
	SumVersion string    `json:"sum_version"`
	Contracts  Contracts `json:"contracts"`
	Supports   Supports  `json:"supports"`
}

func BuildRelease() Release {
	return Release{
		SumVersion: SumVersion,
		Contracts:  Contracts{HerdrCLI: HerdrCLI, MCP: MCP},
		Supports:   Supports{StateSchema: []int{StateSchema}, BriefSchema: []int{BriefSchema}, MachineIdentity: []int{MachineIdentity}, WakeProtocol: []int{WakeProtocol}},
	}
}
