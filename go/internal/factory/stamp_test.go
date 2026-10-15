package factory

import (
	"strings"
	"testing"
)

func TestClaimBody_carriesTheRuntimeMarker(t *testing.T) {
	body := claimBody("host-1", "w1:p1", "t-aaaaaaaaaaaa", "<!-- sum:0.1.0@0123456789abcdef0123456789abcdef01234567 -->")
	want := "<!-- " + ClaimMark + " -->\n<!-- sum:0.1.0@0123456789abcdef0123456789abcdef01234567 -->\nhost: host-1\npane: w1:p1\ntask: t-aaaaaaaaaaaa\nat: "
	if !strings.HasPrefix(body, want) {
		t.Fatalf("claimBody =\n%s\nwant it to start with\n%s", body, want)
	}
}
