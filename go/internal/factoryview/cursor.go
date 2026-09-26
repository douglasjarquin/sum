package factoryview

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/douglasjarquin/sum/go/internal/inboxview"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/returns"
)

const (
	// CursorKind separates this envelope from the wake boundary, which shares the token encoding only.
	CursorKind = "factory-digest"
	// MaxTokenBytes bounds the opaque cursor; coverage that does not fit is dropped and labelled truncated.
	MaxTokenBytes = 16 * 1024

	DeltaChanged   = "leaf changed"
	DeltaNewSource = "new source"
)

// Cursor is the caller-held coverage envelope: which installation and project it binds, one leaf per task
// successfully read, and the outcome identities actually rendered. A read never persists it.
type Cursor struct {
	Schema       int      `json:"schema"`
	Kind         string   `json:"kind"`
	Installation string   `json:"installation"`
	Scope        string   `json:"scope"`
	Project      string   `json:"project"`
	Leaves       []Leaf   `json:"leaves"`
	Outcomes     []string `json:"outcomes"`
	Page         Page     `json:"page"`
}

// Leaf is one task's evidence digest: a revision, never a clock. Evidence is the record count, so a shorter
// history than covered is detected as replaced history rather than read as a delta.
type Leaf struct {
	Task     string `json:"task"`
	Digest   string `json:"digest"`
	Evidence int    `json:"evidence"`
}

type Page struct {
	Limit        int  `json:"limit"`
	Rendered     int  `json:"rendered"`
	Total        int  `json:"total"`
	Continuation bool `json:"continuation"`
	Truncated    bool `json:"truncated"`
}

func (c *Cursor) canonical() []byte {
	copy := *c
	copy.Leaves = append([]Leaf{}, c.Leaves...)
	sort.Slice(copy.Leaves, func(i, j int) bool { return copy.Leaves[i].Task < copy.Leaves[j].Task })
	copy.Outcomes = append([]string{}, c.Outcomes...)
	sort.Strings(copy.Outcomes)
	copy.Page = Page{Limit: c.Page.Limit, Truncated: c.Page.Truncated}
	encoded, _ := json.Marshal(&copy)
	return encoded
}

// Token encodes the cursor. When the bounded token cannot carry the coverage, the coverage is dropped and the
// token says so, so the next read resyncs instead of calling repeated history new.
func (c *Cursor) Token() (string, error) {
	payload := c.canonical()
	if len(payload) == 0 {
		return "", fmt.Errorf("cursor could not be encoded")
	}
	token := returns.EncodeToken(payload)
	if len(token) <= MaxTokenBytes {
		return token, nil
	}
	trimmed := Cursor{Schema: c.Schema, Kind: c.Kind, Installation: c.Installation, Scope: c.Scope, Project: c.Project, Leaves: []Leaf{}, Outcomes: []string{}, Page: Page{Limit: c.Page.Limit, Truncated: true}}
	return returns.EncodeToken(trimmed.canonical()), nil
}

// Parse decodes and self-checks a digest cursor; a wake receipt or any other token never verifies.
func Parse(token string) (*Cursor, error) {
	payload, fingerprint, err := returns.DecodeToken(token)
	if err != nil {
		return nil, fmt.Errorf("not a digest cursor (%s)", err.Error())
	}
	var c Cursor
	if err := json.Unmarshal(payload, &c); err != nil {
		return nil, fmt.Errorf("not a digest cursor (payload is not a cursor)")
	}
	if c.Kind != CursorKind {
		return nil, fmt.Errorf("not a digest cursor (kind %q)", c.Kind)
	}
	if c.Schema != Schema {
		return nil, fmt.Errorf("digest cursor schema %d is not supported", c.Schema)
	}
	if c.Leaves == nil {
		c.Leaves = []Leaf{}
	}
	if c.Outcomes == nil {
		c.Outcomes = []string{}
	}
	if returns.TokenFingerprint(c.canonical()) != fingerprint {
		return nil, fmt.Errorf("digest cursor fingerprint does not match its payload")
	}
	return &c, nil
}

// leafOf digests the task facts the outcomes are derived from; any change to them is a new delta.
func leafOf(task inboxview.Task) Leaf {
	record := task.Record
	picked := ordjson.NewObject()
	picked.Set("status", str(record, "status"))
	picked.Set("report", str(obj(field(record, "report")), "candidate")+"@"+str(obj(field(record, "report")), "submitted_at"))
	evidence := []any{}
	for _, raw := range list(record, "evidence") {
		rec := obj(raw)
		evidence = append(evidence, str(rec, "id")+"|"+str(rec, "kind")+"|"+str(rec, "candidate")+"|"+str(rec, "at"))
	}
	picked.Set("evidence", evidence)
	pr := obj(field(record, "pr"))
	picked.Set("pr", str(pr, "state")+"|"+prNumber(obj(field(pr, "identity")))+"|"+str(pr, "observed_at")+"|"+fmt.Sprint(zero(field(pr, "merge_commit"))))
	cleanupRecord := obj(field(record, "cleanup"))
	picked.Set("cleanup", fmt.Sprint(zero(field(cleanupRecord, "state")))+"|"+str(cleanupRecord, "at"))
	states := []any{}
	for _, key := range []string{"questions", "attention"} {
		for _, raw := range list(record, key) {
			states = append(states, key+":"+str(obj(raw), "id")+"="+str(obj(raw), "status"))
		}
	}
	picked.Set("states", states)
	data, _ := ordjson.MarshalSortedCompact(picked)
	sum := sha256.Sum256(data)
	return Leaf{Task: task.ID, Digest: hex.EncodeToString(sum[:])[:12], Evidence: len(list(record, "evidence"))}
}

// page validates the supplied cursor, labels newness by identity, bounds the page and builds the next cursor.
// Time never decides newness: a late record with an old stamp is new because its identity was never rendered.
func page(out *Digest, all []Outcome, leaves []Leaf, gaps []inboxview.Gap, installation string, opts Options, limit int) []Outcome {
	covered := map[string]bool{}
	current := map[string]Leaf{}
	for _, leaf := range leaves {
		current[leaf.Task] = leaf
	}
	gapped := map[string]bool{}
	for _, gap := range gaps {
		if gap.TaskID != "" {
			gapped[gap.TaskID] = true
		}
	}
	resync := func(reason string) {
		out.Resync = &Resync{Reason: reason}
		out.Deltas = []Delta{}
		covered = map[string]bool{}
	}
	if opts.Since != "" {
		out.Since = true
		cursor, err := Parse(opts.Since)
		switch {
		case err != nil:
			resync(err.Error())
		case cursor.Installation != installation:
			resync("cursor is bound to another installation")
		case cursor.Scope != opts.scope():
			resync(fmt.Sprintf("cursor is bound to scope %q, not %q", cursor.Scope, opts.scope()))
		case cursor.Project != opts.Project:
			resync(fmt.Sprintf("cursor is bound to project %q, not %q", cursor.Project, opts.Project))
		case cursor.Page.Truncated:
			resync("cursor could not retain earlier coverage")
		default:
			seen := map[string]bool{}
			for _, leaf := range cursor.Leaves {
				seen[leaf.Task] = true
				now, readable := current[leaf.Task]
				switch {
				case !readable && gapped[leaf.Task]:
					// Unreadable now: outside coverage, reported as a gap, and back as a new source once readable.
				case !readable:
					resync(fmt.Sprintf("source shrink: task %s is no longer present", leaf.Task))
				case now.Evidence < leaf.Evidence:
					resync(fmt.Sprintf("history replaced: task %s has fewer records than the cursor covered", leaf.Task))
				case now.Digest != leaf.Digest:
					out.Deltas = append(out.Deltas, Delta{Task: leaf.Task, Reason: DeltaChanged})
				}
				if out.Resync != nil {
					break
				}
			}
			if out.Resync == nil {
				for _, leaf := range leaves {
					if !seen[leaf.Task] {
						out.Deltas = append(out.Deltas, Delta{Task: leaf.Task, Reason: DeltaNewSource})
					}
				}
				for _, id := range cursor.Outcomes {
					covered[id] = true
				}
			}
		}
	}
	sort.SliceStable(all, func(i, j int) bool {
		if all[i].Project != all[j].Project {
			return all[i].Project < all[j].Project
		}
		return all[i].Identity < all[j].Identity
	})
	var uncovered, retained []Outcome
	for _, o := range all {
		if covered[o.Identity] {
			retained = append(retained, o)
			continue
		}
		o.New = out.Since && out.Resync == nil
		uncovered = append(uncovered, o)
	}
	rendered := append(append([]Outcome{}, uncovered...), retained...)
	if len(rendered) > limit {
		rendered = rendered[:limit]
	}
	out.Page = Page{Limit: limit, Rendered: len(rendered), Total: len(all), Continuation: len(uncovered) > limit}
	next := Cursor{Schema: Schema, Kind: CursorKind, Installation: installation, Scope: opts.scope(), Project: opts.Project, Leaves: leaves, Outcomes: []string{}, Page: Page{Limit: limit}}
	existing := map[string]bool{}
	for _, o := range all {
		existing[o.Identity] = true
	}
	// Coverage of an identity that no longer exists is pruned only when every source was readable; behind a gap
	// the identity may merely be hidden, and pruning it would relabel it new when the source returns.
	for id := range covered {
		if existing[id] || len(gaps) > 0 {
			next.Outcomes = append(next.Outcomes, id)
		}
	}
	for _, o := range rendered {
		if !covered[o.Identity] {
			next.Outcomes = append(next.Outcomes, o.Identity)
		}
	}
	sort.Strings(next.Outcomes)
	token, err := next.Token()
	if err == nil {
		out.Cursor = token
		if parsed, _ := Parse(token); parsed != nil {
			out.Page.Truncated = parsed.Page.Truncated
		}
	} else if out.Resync == nil {
		out.Resync = &Resync{Reason: err.Error()}
	}
	return rendered
}
