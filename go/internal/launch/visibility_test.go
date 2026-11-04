package launch

import (
	"context"
	"strings"
	"testing"

	"github.com/douglasjarquin/sum/go/internal/hcclient"
	"github.com/douglasjarquin/sum/go/internal/hctest"
)

func TestVisibilityLabelsSeparateInstallationsAndShareATaskTab(t *testing.T) {
	one := VisibilityLabels("0123456789abcdef0123456789abcdef", "t-aaaaaaaaaaaa")
	other := VisibilityLabels("fedcba9876543210fedcba9876543210", "t-aaaaaaaaaaaa")
	if one["herdr.space"] == other["herdr.space"] || one["herdr.tab"] == other["herdr.tab"] {
		t.Fatalf("installations share a space: %v %v", one, other)
	}
	if !strings.HasPrefix(one["herdr.space"], "sum-0123456789abcdef") || !strings.HasPrefix(one["herdr.tab"], one["herdr.space"]+"-t-") {
		t.Fatalf("labels do not derive from installation and task: %v", one)
	}
	worker, reviewer := NewVisibility("0123456789abcdef0123456789abcdef", "t-aaaaaaaaaaaa"), NewVisibility("0123456789abcdef0123456789abcdef", "t-aaaaaaaaaaaa")
	if worker.Labels["herdr.tab"] != reviewer.Labels["herdr.tab"] || worker.State != VisibilityPending {
		t.Fatalf("worker and reviewer intents differ: %+v %+v", worker, reviewer)
	}
	for key := range worker.Labels {
		if !strings.HasPrefix(key, "herdr.") {
			t.Fatalf("visibility intent carries a non-Herdr label %s", key)
		}
	}
}

func TestPublishVisibilityRecordsAppliedUncertainAndRefusedOutcomes(t *testing.T) {
	for _, scenario := range []struct {
		name  string
		fault *hctest.Fault
		want  string
	}{
		{"applied", nil, VisibilityApplied},
		{"lost-reply", &hctest.Fault{LoseResponse: true}, VisibilityUncertain},
		{"refused", &hctest.Fault{RPCError: &hcclient.RPCError{Code: -32000, Message: "not_found"}}, VisibilityRefused},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			fixture := hctest.New(t)
			client := fixture.Client()
			run, err := client.Run(context.Background(), hcclient.RunParams{Argv: []string{"agent"}, Labels: map[string]string{"sum.attempt": "a"}, OutliveParent: true, NoNotify: true})
			if err != nil {
				t.Fatal(err)
			}
			if scenario.fault != nil {
				fixture.QueueFault("terminal.label", *scenario.fault)
			}
			labels := map[string]string{"herdr.space": "sum-x", "herdr.tab": "sum-x-t"}
			claims := 0
			var recorded Visibility
			err = PublishVisibility(context.Background(), &client, run.Address, func() (map[string]string, error) {
				claims++
				return labels, nil
			}, func(outcome Visibility) error {
				recorded = outcome
				return nil
			})
			if err != nil || claims != 1 || recorded.State != scenario.want {
				t.Fatalf("err=%v claims=%d recorded=%+v", err, claims, recorded)
			}
			if scenario.want == VisibilityApplied && (recorded.Observed["herdr.tab"] != "sum-x-t" || fixture.Terminals()[0].Labels["sum.attempt"] != "a") {
				t.Fatalf("applied outcome lost labels: %+v %v", recorded, fixture.Terminals()[0].Labels)
			}
			if scenario.want != VisibilityApplied && recorded.Detail == "" {
				t.Fatalf("outcome without detail: %+v", recorded)
			}
			if observed := ObservedVisibility(Visibility{Labels: labels}, fixture.Terminals()[0]); (observed.State == VisibilityApplied) != (fixture.Terminals()[0].Labels["herdr.space"] == "sum-x") {
				t.Fatalf("observed outcome disagrees with the terminal: %+v", observed)
			}
		})
	}
}

func TestPublishVisibilitySkipsWhenNothingIsClaimed(t *testing.T) {
	fixture := hctest.New(t)
	client := fixture.Client()
	err := PublishVisibility(context.Background(), &client, "t1", func() (map[string]string, error) { return nil, nil }, func(Visibility) error {
		t.Fatal("recorded an outcome without a request")
		return nil
	})
	if err != nil || len(fixture.Calls()) != 0 {
		t.Fatalf("err=%v calls=%v", err, fixture.Calls())
	}
}
