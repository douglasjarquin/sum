package contract

const (
	SumVersion  = "0.1.0"
	StateSchema = 1
	BriefSchema = 1
	HerdrCLI    = "0.9.0"
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
	StateSchema []int `json:"state_schema"`
	BriefSchema []int `json:"brief_schema"`
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
		Supports:   Supports{StateSchema: []int{StateSchema}, BriefSchema: []int{BriefSchema}},
	}
}
