package contract

const (
	SumVersion  = "0.1.0"
	StateSchema = 2
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
	WakeProtocol         = 1
	HCVersion            = "0.1.14"
	HCGeneration         = 1
	EndpointSchema       = 2
	NotificationProtocol = 2
	ObserverProtocol     = 1
)

type Contracts struct {
	HCCore               string `json:"hc_core"`
	HCGeneration         int    `json:"hc_generation"`
	EndpointSchema       int    `json:"endpoint_schema"`
	NotificationProtocol int    `json:"notification_protocol"`
}

type Supports struct {
	EndpointSchema       []int `json:"endpoint_schema"`
	NotificationProtocol []int `json:"notification_protocol"`
	ObserverProtocol     []int `json:"observer_protocol"`
	StateSchema          []int `json:"state_schema"`
	BriefSchema          []int `json:"brief_schema"`
	MachineIdentity      []int `json:"machine_identity"`
	WakeProtocol         []int `json:"wake_protocol"`
}

type Release struct {
	SumVersion string    `json:"sum_version"`
	Contracts  Contracts `json:"contracts"`
	Supports   Supports  `json:"supports"`
}

func BuildRelease() Release {
	return Release{
		SumVersion: SumVersion,
		Contracts:  Contracts{HCCore: HCVersion, HCGeneration: HCGeneration, EndpointSchema: EndpointSchema, NotificationProtocol: NotificationProtocol},
		Supports:   Supports{EndpointSchema: []int{EndpointSchema}, NotificationProtocol: []int{NotificationProtocol}, ObserverProtocol: []int{ObserverProtocol}, StateSchema: []int{StateSchema}, BriefSchema: []int{BriefSchema}, MachineIdentity: []int{MachineIdentity}, WakeProtocol: []int{WakeProtocol}},
	}
}
