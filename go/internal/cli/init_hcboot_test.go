package cli

import "testing"

func TestBareInitNeverEnsuresHC(t *testing.T) {
	root, _ := ordinaryInitLab(t)
	view, err := ordinaryInit(t, root)
	if err != nil || view["role"] != "coordinator" {
		t.Fatalf("ordinary entry = %v %v", view, err)
	}
	if _, ok := view["services"]; ok {
		t.Fatalf("init has runtime side effects: %v", view)
	}
}
