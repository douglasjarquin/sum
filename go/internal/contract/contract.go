package contract

import (
	"strconv"
	"strings"
)

const (
	SumVersion         = "0.1.0"
	StateSchema        = 2
	LogicalStateSchema = 3
	OwnerSchema        = 2
	BriefSchema        = 1
	// MachineIdentity 1 says this binary resolves recorded machine values
	// through internal/machine. Only release-contract reports it: a checkout
	// target runs its prebuilt .local/bin/sumctl, so the binary must say what
	// it is. Staging never copies it into release.json, where it would describe
	// the stager rather than the staged tree.
	MachineIdentity = 1
	// WakeProtocol and ObserverProtocol advertise read-only preservation of historical sidecars.
	// They remain reported so the last native updater can select this release.
	WakeProtocol         = 1
	HCVersion            = "0.1.14"
	HCGeneration         = 1
	EndpointSchema       = 2
	NotificationProtocol = 2
	ObserverProtocol     = 1
	HCTemplateSchema     = 2
)

type Contracts struct {
	HCCore               string `json:"hc_core"`
	HCGeneration         int    `json:"hc_generation"`
	EndpointSchema       int    `json:"endpoint_schema"`
	NotificationProtocol int    `json:"notification_protocol"`
}

type Supports struct {
	HCTemplateSchema     []int `json:"hc_template_schema"`
	EndpointSchema       []int `json:"endpoint_schema"`
	NotificationProtocol []int `json:"notification_protocol"`
	ObserverProtocol     []int `json:"observer_protocol"`
	StateSchema          []int `json:"state_schema"`
	BriefSchema          []int `json:"brief_schema"`
	MachineIdentity      []int `json:"machine_identity"`
	WakeProtocol         []int `json:"wake_protocol"`
	OwnerSchema          []int `json:"owner_schema"`
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
		Supports:   Supports{HCTemplateSchema: []int{0, HCTemplateSchema}, EndpointSchema: []int{EndpointSchema}, NotificationProtocol: []int{NotificationProtocol}, ObserverProtocol: []int{ObserverProtocol}, StateSchema: []int{StateSchema, LogicalStateSchema}, BriefSchema: []int{BriefSchema}, MachineIdentity: []int{MachineIdentity}, WakeProtocol: []int{WakeProtocol}, OwnerSchema: []int{OwnerSchema}},
	}
}

func HCCompatible(version string, generation int) bool {
	if generation != HCGeneration {
		return false
	}
	parse := func(value string) ([]uint64, bool) {
		parts := strings.Split(value, ".")
		numbers := make([]uint64, len(parts))
		for i, part := range parts {
			if part == "" {
				return nil, false
			}
			for _, digit := range part {
				if digit < '0' || digit > '9' {
					return nil, false
				}
			}
			n, err := strconv.ParseUint(part, 10, 64)
			if err != nil {
				return nil, false
			}
			numbers[i] = n
		}
		return numbers, true
	}
	observed, ok := parse(version)
	if !ok {
		return false
	}
	minimum, _ := parse(HCVersion)
	for i := 0; i < len(observed) || i < len(minimum); i++ {
		var a, b uint64
		if i < len(observed) {
			a = observed[i]
		}
		if i < len(minimum) {
			b = minimum[i]
		}
		if a != b {
			return a > b
		}
	}
	return true
}
