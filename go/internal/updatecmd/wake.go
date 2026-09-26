package updatecmd

import (
	"fmt"
	"strings"

	"github.com/douglasjarquin/sum/go/internal/contract"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/returns"
	"github.com/douglasjarquin/sum/go/internal/store"
)

// wakeMarker is the file #240a added with the coordinator wake sidecar. A release tree that contains it keeps that
// sidecar and can reconcile it; one without it prompts the coordinator over an outstanding episode and cannot
// reconcile it. Like identityMarker, the evidence is the target's own tree or its own helper, never a field the
// staging runtime stamps.
const wakeMarker = "go/internal/returns/wake.go"

func releaseWake(manifest *ordjson.Object) targetIdentity {
	files := asObject(func() any {
		if manifest == nil {
			return nil
		}
		v, _ := manifest.Get("files")
		return v
	}())
	offers := false
	if files != nil {
		_, offers = files.Get(wakeMarker)
	}
	return targetIdentity{
		offers:   offers,
		evidence: "release files: " + wakeMarker,
		lacking:  fmt.Sprintf("its tree has no %s, which #240a added", wakeMarker),
	}
}

func checkoutWake(helperContract *ordjson.Object) targetIdentity {
	var offered []any
	if supports := asObject(func() any {
		if helperContract == nil {
			return nil
		}
		v, _ := helperContract.Get("supports")
		return v
	}()); supports != nil {
		v, _ := supports.Get("wake_protocol")
		offered, _ = v.([]any)
	}
	return targetIdentity{
		offers:   containsNumber(offered, contract.WakeProtocol),
		evidence: "checkout helper release-contract: supports.wake_protocol",
		lacking:  fmt.Sprintf("the checkout's .local/bin/sumctl does not report supports.wake_protocol %d in its release-contract; if HEAD is newer than that build, rebuild it with `mise run test` and retry", contract.WakeProtocol),
	}
}

// wakeCompatibility reports whether the target can be selected over this installation's wake sidecars: an
// outstanding, uncertain, unreconciled, or unreadable episode refuses a target without the protocol, with no
// override, because that code would prompt over the episode and could not reconcile it.
func wakeCompatibility(s *store.Store, target targetIdentity, servesNow bool) (*ordjson.Object, string, error) {
	entries, err := returns.ListWakes(s)
	if err != nil {
		return nil, "", err
	}
	row := ordjson.NewObject()
	row.Set("target_evidence", target.evidence)
	row.Set("target_offers", target.offers)
	var episodes []any
	var unresolved []string
	for _, entry := range entries {
		item := ordjson.NewObject()
		item.Set("path", entry.Path)
		switch {
		case entry.Status.State != returns.WakeOK:
			item.Set("state", entry.Status.State)
			item.Set("diagnostic", entry.Status.Diagnostic)
			unresolved = append(unresolved, fmt.Sprintf("sidecar %s is %s; sum never rewrites it. Inspect it (`sumctl wake show`) and move or remove the file by hand, then retry", entry.Path, blockedReason(entry.Status)))
		default:
			w := entry.Wake
			item.Set("recipient", fmt.Sprintf("%s %s on %s", w.Recipient.Session, w.Recipient.Pane, w.Recipient.Machine))
			item.Set("generation", jsonNumber(w.Generation))
			if w.Episode == nil {
				item.Set("episode", nil)
				item.Set("phase", nil)
			} else {
				item.Set("episode", w.Episode.ID)
				item.Set("phase", w.Episode.Phase)
			}
			if w.IsOutstanding() || w.NeedsReconcile() {
				unresolved = append(unresolved, fmt.Sprintf("coordinator %s %s on %s: episode %s is %s", w.Recipient.Session, w.Recipient.Pane, w.Recipient.Machine, w.Episode.ID, w.Episode.Phase))
			}
		}
		episodes = append(episodes, item)
	}
	if episodes == nil {
		episodes = []any{}
	}
	row.Set("episodes", episodes)
	var blocking string
	switch {
	case target.offers:
		row.Set("result", "supported")
	case len(unresolved) == 0:
		row.Set("result", "unused")
	case servesNow:
		row.Set("result", "serving")
	default:
		row.Set("result", "refused")
		blocking = fmt.Sprintf("the target has no coordinator wake protocol (%s), but this installation has a routine wake that only this protocol can settle: %s. That code would prompt the coordinator over it and cannot reconcile it. First settle the episode from the serving runtime: `sumctl wake consume` after the coordinator has read its wake, or `sumctl wake reconcile` for one that is prepared or uncertain (`sumctl wake show` inspects it); an unreadable or foreign sidecar is never rewritten by sum: move or remove that file by hand. There is no override flag.", target.lacking, strings.Join(unresolved, "; "))
	}
	return row, blocking, nil
}

// blockedReason is the sidecar's diagnostic without the path prefix and the inspection sentence readWakeFile adds,
// so the refusal can name the file once and say what to do with it.
func blockedReason(status returns.WakeStatus) string {
	reason := strings.TrimPrefix(status.Diagnostic, status.Path+" ")
	if i := strings.Index(reason, "; inspect it with"); i >= 0 {
		reason = reason[:i]
	}
	return reason
}
