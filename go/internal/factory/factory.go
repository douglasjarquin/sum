package factory

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/douglasjarquin/sum/go/internal/evidenceview"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/pipeline"
	"github.com/douglasjarquin/sum/go/internal/proc"
	"github.com/douglasjarquin/sum/go/internal/project"
	"github.com/douglasjarquin/sum/go/internal/store"
	"github.com/douglasjarquin/sum/go/internal/toolpath"
)

const (
	File   = "factory.json"
	Schema = 1

	ClaimLabel = "sum-claimed"
	GateLabel  = "sum-gated"
	ClaimMark  = "sum-factory-claim"

	ReadyLabel         = "label"
	ReadyIssues        = "issues"
	ReadyRoadmap       = "roadmap"
	ReadyProjectStatus = "project-status"

	ActionIdle     = "idle"
	ActionDispatch = "dispatch"
	ActionOccupied = "occupied"
	ActionBlocked  = "blocked"
	ActionDisabled = "disabled"
	ActionNone     = "none"

	LaneRunning = "running"
	LaneGated   = "gated"
	LaneClaimed = "claimed"

	ReasonGated   = "gated"
	ReasonMerged  = "merged"
	ReasonDropped = "dropped"

	DefaultIdleSeconds = 300
	DefaultLanes       = 1
)

var AuthorizedMerge = []string{
	"douglasjarquin/remainder",
	"cofactorworks/nicebaas",
	"cofactorworks/ilovethatphoto",
}

var (
	projectNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,99}/[A-Za-z0-9][A-Za-z0-9._-]{0,99}$`)
	roadmapIssuePat    = regexp.MustCompile(`\|\s*\d+\s*\|\s*#(\d+)`)
	hashIssuePat       = regexp.MustCompile(`#(\d+)`)
)

type EnableArgs struct {
	Project       string
	Lanes         int
	Ready         string
	Label         string
	RoadmapIssue  int
	ProjectNumber int
	ReadyOption   string
	IdleSeconds   int
	StrictCleanup bool
	Skip          []int
}

type ClaimArgs struct {
	Project string
	Issue   int
	Task    string
}

type ReleaseArgs struct {
	Project  string
	Issue    int
	Reason   string
	Continue bool
}

func jsonInt(n int) json.Number {
	return json.Number(fmt.Sprint(n))
}

func asObject(v any) *ordjson.Object {
	o, _ := v.(*ordjson.Object)
	return o
}

func asString(v any) string {
	s, _ := v.(string)
	return s
}

func asBool(v any) bool {
	b, _ := v.(bool)
	return b
}

func asInt(v any) int {
	switch t := v.(type) {
	case json.Number:
		n, _ := t.Int64()
		return int(n)
	case float64:
		return int(t)
	case int:
		return t
	default:
		return 0
	}
}

func pathFile(s *store.Store) string {
	return filepath.Join(s.Home, File)
}

func emptyRegistry() *ordjson.Object {
	reg := ordjson.NewObject()
	reg.Set("schema", jsonInt(Schema))
	reg.Set("projects", ordjson.NewObject())
	return reg
}

func Read(s *store.Store) (*ordjson.Object, error) {
	path := pathFile(s)
	info, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return emptyRegistry(), nil
		}
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("%s must not be a symlink", path)
	}
	value, readErr := ordjson.ReadFile(path)
	if readErr != nil {
		return nil, readErr
	}
	obj := asObject(value)
	if obj == nil {
		return nil, fmt.Errorf("unsupported factory registry %s; preserve it and use the matching sum release", path)
	}
	schema, _ := obj.Get("schema")
	projects, has := obj.Get("projects")
	if asInt(schema) != Schema || !has || asObject(projects) == nil {
		return nil, fmt.Errorf("unsupported factory registry %s; preserve it and use the matching sum release", path)
	}
	return obj, nil
}

func write(s *store.Store, reg *ordjson.Object) error {
	unlock, err := s.Lock()
	if err != nil {
		return err
	}
	defer unlock()
	return ordjson.WriteFile(pathFile(s), reg)
}

func projectsOf(reg *ordjson.Object) *ordjson.Object {
	return asObject(get(reg, "projects"))
}

func get(o *ordjson.Object, key string) any {
	if o == nil {
		return nil
	}
	v, _ := o.Get(key)
	return v
}

func projectRecord(reg *ordjson.Object, name string) *ordjson.Object {
	return asObject(get(projectsOf(reg), name))
}

func normalizeProject(name string) (string, error) {
	name = strings.TrimSpace(name)
	if !projectNamePattern.MatchString(name) {
		return "", fmt.Errorf("factory project must be owner/repo, got %q", name)
	}
	return name, nil
}

func requireEnrolled(s *store.Store, name string) error {
	registry, err := project.ReadProjects(s)
	if err != nil {
		return err
	}
	projects := asObject(get(registry, "projects"))
	if projects == nil || asObject(get(projects, name)) == nil {
		return fmt.Errorf("No enrolled project %s. Run `sumctl project enroll %s` first.", name, name)
	}
	return nil
}

func Status(s *store.Store, name string) (*ordjson.Object, error) {
	reg, err := Read(s)
	if err != nil {
		return nil, err
	}
	result := ordjson.NewObject()
	result.Set("schema", jsonInt(Schema))
	if name != "" {
		normalized, normErr := normalizeProject(name)
		if normErr != nil {
			return nil, normErr
		}
		rec := projectRecord(reg, normalized)
		if rec == nil {
			return nil, fmt.Errorf("no factory enabled for %s", normalized)
		}
		result.Set("project", summary(rec))
		result.Set("projects", []any{summary(rec)})
		return result, nil
	}
	obj := projectsOf(reg)
	names := obj.Keys()
	sort.Strings(names)
	rows := make([]any, 0, len(names))
	for _, n := range names {
		rows = append(rows, summary(asObject(get(obj, n))))
	}
	result.Set("projects", rows)
	result.Set("note", "Lane state only; tick observes GitHub. Nothing was dispatched.")
	return result, nil
}

func summary(rec *ordjson.Object) *ordjson.Object {
	out := ordjson.NewObject()
	if rec == nil {
		return out
	}
	for _, key := range []string{"name", "enabled", "lanes", "ready", "idle_seconds", "strict_cleanup", "last_tick_at", "next_tick_at", "lanes_held", "skip"} {
		out.Set(key, get(rec, key))
	}
	out.Set("held", heldCount(rec))
	out.Set("free", asInt(get(rec, "lanes"))-heldCount(rec))
	return out
}

func heldCount(rec *ordjson.Object) int {
	n := 0
	for _, lane := range lanes(rec) {
		state := asString(get(lane, "state"))
		if state == LaneRunning || state == LaneClaimed || state == LaneGated {
			n++
		}
	}
	return n
}

func lanes(rec *ordjson.Object) []*ordjson.Object {
	raw, _ := rec.Get("lanes_held")
	list, _ := raw.([]any)
	out := make([]*ordjson.Object, 0, len(list))
	for _, item := range list {
		if o := asObject(item); o != nil {
			out = append(out, o)
		}
	}
	return out
}

func Enable(s *store.Store, ctx *ordjson.Object, args EnableArgs) (*ordjson.Object, error) {
	name, err := normalizeProject(args.Project)
	if err != nil {
		return nil, err
	}
	if err := requireEnrolled(s, name); err != nil {
		return nil, err
	}
	ready := args.Ready
	if ready == "" {
		ready = ReadyLabel
	}
	if ready != ReadyLabel && ready != ReadyIssues && ready != ReadyRoadmap && ready != ReadyProjectStatus {
		return nil, fmt.Errorf("--ready must be label, issues, roadmap, or project-status")
	}
	if ready == ReadyRoadmap && args.RoadmapIssue <= 0 {
		return nil, fmt.Errorf("roadmap ready signal needs --roadmap-issue")
	}
	lanesN := args.Lanes
	if lanesN <= 0 {
		lanesN = DefaultLanes
	}
	idle := args.IdleSeconds
	if idle <= 0 {
		idle = DefaultIdleSeconds
	}
	label := args.Label
	if label == "" && ready == ReadyLabel {
		label = "ready"
	}
	readyOpt := args.ReadyOption
	if readyOpt == "" {
		readyOpt = "Ready"
	}
	reg, err := Read(s)
	if err != nil {
		return nil, err
	}
	rec := projectRecord(reg, name)
	if rec == nil {
		rec = ordjson.NewObject()
		rec.Set("lanes_held", []any{})
	}
	rec.Set("name", name)
	rec.Set("enabled", true)
	rec.Set("lanes", jsonInt(lanesN))
	readyObj := ordjson.NewObject()
	readyObj.Set("kind", ready)
	readyObj.Set("label", label)
	if args.RoadmapIssue > 0 {
		readyObj.Set("roadmap_issue", jsonInt(args.RoadmapIssue))
	} else {
		readyObj.Set("roadmap_issue", nil)
	}
	if args.ProjectNumber > 0 {
		readyObj.Set("project_number", jsonInt(args.ProjectNumber))
	} else {
		readyObj.Set("project_number", nil)
	}
	readyObj.Set("ready_option", readyOpt)
	rec.Set("ready", readyObj)
	rec.Set("idle_seconds", jsonInt(idle))
	rec.Set("strict_cleanup", args.StrictCleanup)
	skip := make([]any, 0, len(args.Skip))
	for _, n := range args.Skip {
		if n > 0 {
			skip = append(skip, jsonInt(n))
		}
	}
	rec.Set("skip", skip)
	rec.Set("enabled_at", store.Now())
	if ctx != nil {
		by := ordjson.NewObject()
		by.Set("machine", get(ctx, "machine"))
		by.Set("session", get(ctx, "session"))
		by.Set("pane", get(ctx, "pane"))
		rec.Set("enabled_by", by)
	}
	projects := projectsOf(reg)
	projects.Set(name, rec)
	if err := write(s, reg); err != nil {
		return nil, err
	}
	result := ordjson.NewObject()
	result.Set("project", summary(rec))
	result.Set("note", "Factory enabled. Tick observes GitHub; dispatch still belongs to the coordinator.")
	return result, nil
}

func Disable(s *store.Store, name string) (*ordjson.Object, error) {
	normalized, err := normalizeProject(name)
	if err != nil {
		return nil, err
	}
	reg, err := Read(s)
	if err != nil {
		return nil, err
	}
	rec := projectRecord(reg, normalized)
	if rec == nil {
		return nil, fmt.Errorf("no factory enabled for %s", normalized)
	}
	if heldCount(rec) > 0 {
		return nil, fmt.Errorf("Refusing disable: %s still holds %d lane(s). Release or merge them first.", normalized, heldCount(rec))
	}
	rec.Set("enabled", false)
	rec.Set("disabled_at", store.Now())
	projectsOf(reg).Set(normalized, rec)
	if err := write(s, reg); err != nil {
		return nil, err
	}
	result := ordjson.NewObject()
	result.Set("project", summary(rec))
	result.Set("note", "Factory disabled. Existing GitHub claims are unchanged.")
	return result, nil
}

func Tick(s *store.Store, runtimeRoot, name string) (*ordjson.Object, error) {
	reg, err := Read(s)
	if err != nil {
		return nil, err
	}
	var names []string
	if name != "" {
		normalized, normErr := normalizeProject(name)
		if normErr != nil {
			return nil, normErr
		}
		if projectRecord(reg, normalized) == nil {
			return nil, fmt.Errorf("no factory enabled for %s", normalized)
		}
		names = []string{normalized}
	} else {
		names = projectsOf(reg).Keys()
		sort.Strings(names)
	}
	rows := make([]any, 0, len(names))
	changed := false
	for _, n := range names {
		rec := projectRecord(reg, n)
		row, wrote, tickErr := tickOne(s, runtimeRoot, rec)
		if tickErr != nil {
			fail := ordjson.NewObject()
			fail.Set("name", n)
			fail.Set("action", ActionBlocked)
			fail.Set("error", tickErr.Error())
			rows = append(rows, fail)
			continue
		}
		rows = append(rows, row)
		if wrote {
			changed = true
		}
	}
	if changed {
		if err := write(s, reg); err != nil {
			return nil, err
		}
	}
	result := ordjson.NewObject()
	result.Set("ticks", rows)
	result.Set("note", "One observation. Nothing slept, dispatched, or merged.")
	return result, nil
}

func tickOne(s *store.Store, runtimeRoot string, rec *ordjson.Object) (*ordjson.Object, bool, error) {
	name := asString(get(rec, "name"))
	row := ordjson.NewObject()
	row.Set("name", name)
	if !asBool(get(rec, "enabled")) {
		row.Set("action", ActionDisabled)
		return row, false, nil
	}
	if heldCount(rec) >= asInt(get(rec, "lanes")) {
		row.Set("action", ActionOccupied)
		row.Set("held", jsonInt(heldCount(rec)))
		row.Set("lanes", get(rec, "lanes"))
		return row, false, nil
	}
	issues, signal, err := listReady(runtimeRoot, rec)
	if err != nil {
		row.Set("action", ActionBlocked)
		row.Set("ready_signal", signal)
		row.Set("error", err.Error())
		stampIdle(rec)
		return row, true, nil
	}
	row.Set("ready_signal", signal)
	next := firstUnclaimed(rec, issues)
	stamp := store.Now()
	rec.Set("last_tick_at", stamp)
	if next == 0 {
		idle := asInt(get(rec, "idle_seconds"))
		if idle <= 0 {
			idle = DefaultIdleSeconds
		}
		nextAt := store.NowTime().UTC().Add(time.Duration(idle) * time.Second).Format("2006-01-02T15:04:05+00:00")
		rec.Set("next_tick_at", nextAt)
		row.Set("action", ActionIdle)
		row.Set("next_tick_at", nextAt)
		return row, true, nil
	}
	rec.Set("next_tick_at", nil)
	row.Set("action", ActionDispatch)
	row.Set("issue", jsonInt(next))
	row.Set("title", issueTitle(issues, next))
	return row, true, nil
}

func stampIdle(rec *ordjson.Object) {
	rec.Set("last_tick_at", store.Now())
	idle := asInt(get(rec, "idle_seconds"))
	if idle <= 0 {
		idle = DefaultIdleSeconds
	}
	rec.Set("next_tick_at", store.NowTime().UTC().Add(time.Duration(idle)*time.Second).Format("2006-01-02T15:04:05+00:00"))
}

type ghIssue struct {
	Number int
	Title  string
	State  string
	Labels []string
}

func listReady(runtimeRoot string, rec *ordjson.Object) ([]ghIssue, string, error) {
	ready := asObject(get(rec, "ready"))
	kind := asString(get(ready, "kind"))
	if kind == "" {
		kind = ReadyLabel
	}
	name := asString(get(rec, "name"))
	switch kind {
	case ReadyLabel:
		label := asString(get(ready, "label"))
		if label == "" {
			label = "ready"
		}
		issues, err := ghIssueList(runtimeRoot, name, label)
		return issues, kind, err
	case ReadyIssues:
		issues, err := ghIssueList(runtimeRoot, name, "")
		return issues, kind, err
	case ReadyRoadmap:
		parent := asInt(get(ready, "roadmap_issue"))
		if parent <= 0 {
			return nil, kind, fmt.Errorf("roadmap ready signal has no --roadmap-issue")
		}
		issues, err := ghRoadmap(runtimeRoot, name, parent)
		return issues, kind, err
	case ReadyProjectStatus:
		number := asInt(get(ready, "project_number"))
		if number <= 0 {
			return nil, kind, fmt.Errorf("project-status ready signal needs a GitHub Project number")
		}
		option := asString(get(ready, "ready_option"))
		if option == "" {
			option = "Ready"
		}
		issues, err := ghProjectReady(runtimeRoot, name, number, option)
		return issues, kind, err
	default:
		return nil, kind, fmt.Errorf("unknown ready kind %s", kind)
	}
}

func skipSet(rec *ordjson.Object) map[int]bool {
	out := map[int]bool{}
	raw, _ := rec.Get("skip")
	list, _ := raw.([]any)
	for _, item := range list {
		if n := asInt(item); n > 0 {
			out[n] = true
		}
	}
	return out
}

func claimedSet(rec *ordjson.Object) map[int]bool {
	out := map[int]bool{}
	for _, lane := range lanes(rec) {
		n := asInt(get(lane, "issue"))
		if n > 0 {
			out[n] = true
		}
	}
	return out
}

func firstUnclaimed(rec *ordjson.Object, issues []ghIssue) int {
	skip := skipSet(rec)
	claimed := claimedSet(rec)
	for _, issue := range issues {
		if issue.Number <= 0 || strings.ToLower(issue.State) != "open" {
			continue
		}
		if skip[issue.Number] || claimed[issue.Number] || hasLabel(issue, ClaimLabel) || hasLabel(issue, GateLabel) {
			continue
		}
		return issue.Number
	}
	return 0
}

func hasLabel(issue ghIssue, name string) bool {
	for _, label := range issue.Labels {
		if label == name {
			return true
		}
	}
	return false
}

func issueTitle(issues []ghIssue, number int) string {
	for _, issue := range issues {
		if issue.Number == number {
			return issue.Title
		}
	}
	return ""
}

func ghBin(runtimeRoot string) (string, error) {
	return toolpath.Find(runtimeRoot, "gh")
}

func runGh(runtimeRoot string, args ...string) ([]byte, error) {
	bin, err := ghBin(runtimeRoot)
	if err != nil {
		return nil, err
	}
	res, runErr := proc.RunContext(context.Background(), proc.Cmd{Argv: append([]string{bin}, args...), Timeout: pipeline.DefaultGHBound})
	if runErr != nil || res.Code != 0 {
		text := res.Detail()
		if strings.Contains(text, "read:project") || strings.Contains(strings.ToLower(text), "insufficient_scopes") {
			return nil, fmt.Errorf("GitHub Projects need the read:project scope. Run `gh auth refresh -s read:project`. %s", text)
		}
		if runErr != nil {
			text = strings.TrimSpace(runErr.Error() + " " + text)
		}
		return nil, fmt.Errorf("gh %s: %s", strings.Join(args, " "), text)
	}
	return []byte(res.Stdout), nil
}

func ghIssueList(runtimeRoot, repo, label string) ([]ghIssue, error) {
	args := []string{"issue", "list", "--repo", repo, "--state", "open", "--json", "number,title,state,labels", "--sort", "created", "--order", "asc", "--limit", "100", "--paginate"}
	if label != "" {
		args = append(args, "--label", label)
	}
	out, err := runGh(runtimeRoot, args...)
	if err != nil {
		return nil, err
	}
	return decodeIssueList(out)
}

func decodeIssueList(raw []byte) ([]ghIssue, error) {
	var rows []map[string]any
	if err := json.Unmarshal(raw, &rows); err != nil {
		return nil, fmt.Errorf("gh did not return an issue list: %s", truncate(string(raw), 300))
	}
	issues := make([]ghIssue, 0, len(rows))
	for _, row := range rows {
		issue := ghIssue{
			Number: jsonNumber(row["number"]),
			Title:  fmt.Sprint(zero(row["title"])),
			State:  strings.ToLower(fmt.Sprint(zero(row["state"]))),
		}
		if issue.State == "" {
			issue.State = "open"
		}
		if labels, ok := row["labels"].([]any); ok {
			for _, item := range labels {
				switch t := item.(type) {
				case string:
					issue.Labels = append(issue.Labels, t)
				case map[string]any:
					if name, _ := t["name"].(string); name != "" {
						issue.Labels = append(issue.Labels, name)
					}
				}
			}
		}
		issues = append(issues, issue)
	}
	sort.Slice(issues, func(i, j int) bool { return issues[i].Number < issues[j].Number })
	return issues, nil
}

func jsonNumber(v any) int {
	switch t := v.(type) {
	case float64:
		return int(t)
	case json.Number:
		n, _ := t.Int64()
		return int(n)
	case int:
		return t
	default:
		return 0
	}
}

func zero(v any) any {
	if v == nil {
		return ""
	}
	return v
}

func ghRoadmap(runtimeRoot, repo string, parent int) ([]ghIssue, error) {
	out, err := runGh(runtimeRoot, "issue", "view", fmt.Sprint(parent), "--repo", repo, "--json", "number,title,state,body")
	if err != nil {
		return nil, err
	}
	var payload map[string]any
	if err := json.Unmarshal(out, &payload); err != nil {
		return nil, fmt.Errorf("gh did not return issue JSON: %s", truncate(string(out), 300))
	}
	body, _ := payload["body"].(string)
	order := roadmapOrder(body)
	open, err := ghIssueList(runtimeRoot, repo, "")
	if err != nil {
		return nil, err
	}
	byNumber := map[int]ghIssue{}
	for _, issue := range open {
		byNumber[issue.Number] = issue
	}
	ordered := make([]ghIssue, 0, len(order))
	seen := map[int]bool{}
	for _, n := range order {
		if seen[n] {
			continue
		}
		seen[n] = true
		if issue, ok := byNumber[n]; ok {
			ordered = append(ordered, issue)
		}
	}
	return ordered, nil
}

func roadmapOrder(body string) []int {
	var order []int
	seen := map[int]bool{}
	for _, match := range roadmapIssuePat.FindAllStringSubmatch(body, -1) {
		n, _ := strconv.Atoi(match[1])
		if n > 0 && !seen[n] {
			seen[n] = true
			order = append(order, n)
		}
	}
	if len(order) > 0 {
		return order
	}
	for _, match := range hashIssuePat.FindAllStringSubmatch(body, -1) {
		n, _ := strconv.Atoi(match[1])
		if n > 0 && !seen[n] {
			seen[n] = true
			order = append(order, n)
		}
	}
	return order
}

func ghProjectReady(runtimeRoot, repo string, number int, option string) ([]ghIssue, error) {
	owner, _, _ := strings.Cut(repo, "/")
	out, err := runGh(runtimeRoot, "project", "item-list", fmt.Sprint(number), "--owner", owner, "--format", "json", "--limit", "100")
	if err != nil {
		return nil, err
	}
	var parsed any
	if err := json.Unmarshal(out, &parsed); err != nil {
		return nil, fmt.Errorf("gh did not return project items: %s", truncate(string(out), 300))
	}
	want := repoIdentity(repo)
	items := projectItems(parsed)
	var issues []ghIssue
	for _, item := range items {
		status := strings.TrimSpace(fmt.Sprint(item["status"]))
		if status == "" {
			status = strings.TrimSpace(fmt.Sprint(item["Status"]))
		}
		if !strings.EqualFold(status, option) {
			continue
		}
		if repoIdentity(itemRepository(item)) != want {
			continue
		}
		content, _ := item["content"].(map[string]any)
		if content == nil {
			content = item
		}
		n := jsonNumber(content["number"])
		if n <= 0 {
			continue
		}
		title, _ := content["title"].(string)
		state, _ := content["state"].(string)
		if state == "" {
			state = "open"
		}
		issues = append(issues, ghIssue{Number: n, Title: title, State: strings.ToLower(state)})
	}
	sort.Slice(issues, func(i, j int) bool { return issues[i].Number < issues[j].Number })
	return issues, nil
}

func itemRepository(item map[string]any) string {
	if item == nil {
		return ""
	}
	if s := repositoryString(item["repository"]); s != "" {
		return s
	}
	content, _ := item["content"].(map[string]any)
	if content == nil {
		return ""
	}
	return repositoryString(content["repository"])
}

func repositoryString(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case map[string]any:
		for _, key := range []string{"url", "nameWithOwner", "name"} {
			if s, ok := t[key].(string); ok && s != "" {
				return s
			}
		}
	}
	return ""
}

func repoIdentity(raw string) string {
	s := strings.TrimSpace(raw)
	s = strings.TrimSuffix(s, "/")
	s = strings.TrimSuffix(s, ".git")
	s = strings.ToLower(s)
	for _, prefix := range []string{"https://github.com/", "http://github.com/", "github.com/"} {
		s = strings.TrimPrefix(s, prefix)
	}
	owner, rest, ok := strings.Cut(s, "/")
	if !ok || owner == "" {
		return ""
	}
	repo, _, _ := strings.Cut(rest, "/")
	repo = strings.TrimSuffix(repo, ".git")
	if repo == "" {
		return ""
	}
	return owner + "/" + repo
}

func projectItems(parsed any) []map[string]any {
	switch t := parsed.(type) {
	case []any:
		out := make([]map[string]any, 0, len(t))
		for _, item := range t {
			if m, ok := item.(map[string]any); ok {
				out = append(out, m)
			}
		}
		return out
	case map[string]any:
		for _, key := range []string{"items", "nodes"} {
			if inner, ok := t[key]; ok {
				return projectItems(inner)
			}
		}
	}
	return nil
}

func Claim(s *store.Store, ctx *ordjson.Object, runtimeRoot string, args ClaimArgs) (*ordjson.Object, error) {
	name, err := normalizeProject(args.Project)
	if err != nil {
		return nil, err
	}
	if args.Issue <= 0 {
		return nil, fmt.Errorf("--issue is required")
	}
	reg, err := Read(s)
	if err != nil {
		return nil, err
	}
	rec := projectRecord(reg, name)
	if rec == nil || !asBool(get(rec, "enabled")) {
		return nil, fmt.Errorf("no enabled factory for %s", name)
	}
	existing := laneFor(rec, args.Issue)
	if existing == nil && heldCount(rec) >= asInt(get(rec, "lanes")) {
		return nil, fmt.Errorf("lane limit %d reached for %s", asInt(get(rec, "lanes")), name)
	}
	host := asString(get(ctx, "machine"))
	pane := asString(get(ctx, "pane"))
	if host == "" {
		host, _ = os.Hostname()
	}
	body := claimBody(host, pane, args.Task)
	undoLabel := func() error {
		_, err := runGh(runtimeRoot, "issue", "edit", fmt.Sprint(args.Issue), "--repo", name, "--remove-label", ClaimLabel)
		return err
	}
	if _, err := runGh(runtimeRoot, "issue", "edit", fmt.Sprint(args.Issue), "--repo", name, "--add-label", ClaimLabel); err != nil {
		return nil, err
	}
	tmp, err := os.CreateTemp("", "sum-factory-claim-*.md")
	if err != nil {
		_ = undoLabel()
		return nil, err
	}
	tmpPath := tmp.Name()
	if _, err := tmp.WriteString(body); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		_ = undoLabel()
		return nil, err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		_ = undoLabel()
		return nil, err
	}
	defer os.Remove(tmpPath)
	if _, err := runGh(runtimeRoot, "issue", "comment", fmt.Sprint(args.Issue), "--repo", name, "--body-file", tmpPath); err != nil {
		if undoErr := undoLabel(); undoErr != nil {
			return nil, fmt.Errorf("claim comment failed: %w; also failed to remove %s: %v", err, ClaimLabel, undoErr)
		}
		return nil, fmt.Errorf("claim comment failed; removed %s: %w", ClaimLabel, err)
	}
	lane := existing
	if lane == nil {
		lane = ordjson.NewObject()
		held := laneSlice(rec)
		held = append(held, lane)
		rec.Set("lanes_held", held)
	}
	lane.Set("issue", jsonInt(args.Issue))
	state := LaneClaimed
	if args.Task != "" {
		state = LaneRunning
	}
	lane.Set("state", state)
	lane.Set("claimed_at", store.Now())
	if args.Task != "" {
		lane.Set("task", args.Task)
	}
	claim := ordjson.NewObject()
	claim.Set("host", host)
	claim.Set("pane", pane)
	claim.Set("label", ClaimLabel)
	lane.Set("claim", claim)
	if err := write(s, reg); err != nil {
		return nil, fmt.Errorf("GitHub claim is in place on %s#%d (label %s); lane record failed: %w. Release or clear the label before retrying.", name, args.Issue, ClaimLabel, err)
	}
	result := ordjson.NewObject()
	result.Set("project", summary(rec))
	result.Set("issue", jsonInt(args.Issue))
	result.Set("claim", claim)
	result.Set("task", nilIfEmpty(args.Task))
	result.Set("note", "Issue labeled "+ClaimLabel+". Dispatch remains a separate coordinator command.")
	return result, nil
}

func claimBody(host, pane, task string) string {
	if task == "" {
		task = "none"
	}
	return fmt.Sprintf("<!-- %s -->\nhost: %s\npane: %s\ntask: %s\nat: %s\n", ClaimMark, host, pane, task, store.Now())
}

func laneFor(rec *ordjson.Object, issue int) *ordjson.Object {
	for _, lane := range lanes(rec) {
		if asInt(get(lane, "issue")) == issue {
			return lane
		}
	}
	return nil
}

func laneSlice(rec *ordjson.Object) []any {
	raw, _ := rec.Get("lanes_held")
	list, _ := raw.([]any)
	if list == nil {
		return []any{}
	}
	return append([]any{}, list...)
}

func Release(s *store.Store, args ReleaseArgs) (*ordjson.Object, error) {
	name, err := normalizeProject(args.Project)
	if err != nil {
		return nil, err
	}
	if args.Issue <= 0 {
		return nil, fmt.Errorf("--issue is required")
	}
	reason := args.Reason
	if reason != ReasonGated && reason != ReasonMerged && reason != ReasonDropped {
		return nil, fmt.Errorf("--reason must be gated, merged, or dropped")
	}
	reg, err := Read(s)
	if err != nil {
		return nil, err
	}
	rec := projectRecord(reg, name)
	if rec == nil {
		return nil, fmt.Errorf("no factory enabled for %s", name)
	}
	held := lanes(rec)
	var kept []any
	var found *ordjson.Object
	free := args.Reason == ReasonMerged || args.Reason == ReasonDropped || args.Continue
	for _, lane := range held {
		if asInt(get(lane, "issue")) != args.Issue {
			kept = append(kept, lane)
			continue
		}
		found = lane
		if free {
			continue
		}
		lane.Set("state", LaneGated)
		lane.Set("gated_at", store.Now())
		kept = append(kept, lane)
	}
	if found == nil {
		return nil, fmt.Errorf("issue #%d is not occupying a lane on %s", args.Issue, name)
	}
	if kept == nil {
		kept = []any{}
	}
	rec.Set("lanes_held", kept)
	if err := write(s, reg); err != nil {
		return nil, err
	}
	result := ordjson.NewObject()
	result.Set("project", summary(rec))
	result.Set("issue", jsonInt(args.Issue))
	result.Set("reason", reason)
	result.Set("freed", free)
	result.Set("note", "Lane bookkeeping only. GitHub labels stay unless the coordinator edits them.")
	return result, nil
}

func MergeCheck(s *store.Store, taskID string) (*ordjson.Object, error) {
	task, err := s.ReadTask(taskID)
	if err != nil {
		return nil, err
	}
	view := evidenceview.View(task)
	checks := ordjson.NewObject()
	reasons := []any{}

	repo := authorizedRepo(task)
	authorized := repo != "" && isAuthorized(repo)
	setCheck(checks, "authorized_repo", authorized, repo)
	if !authorized {
		reasons = append(reasons, "repository is outside the standing factory merge authorization")
	}

	missing := anyStrings(get(asObject(get(view, "closure")), "missing"))
	closurePass := len(missing) == 0
	setCheck(checks, "closure", closurePass, strings.Join(missing, "; "))
	if !closurePass {
		reasons = append(reasons, "delivery closure still missing: "+strings.Join(missing, "; "))
	}

	reviewPass := reviewApproved(task)
	setCheck(checks, "independent_review", reviewPass, "")
	if !reviewPass {
		reasons = append(reasons, "independent review is not approve for the current candidate")
	}

	pipePass, pipeDetail := pipelineGatesOK(s, taskID)
	setCheck(checks, "pipeline", pipePass, pipeDetail)
	if !pipePass {
		reasons = append(reasons, "pipeline gates are not ready: "+pipeDetail)
	}

	evidencePass, evidenceDetail := evidenceOK(task)
	setCheck(checks, "evidence", evidencePass, evidenceDetail)
	if !evidencePass {
		reasons = append(reasons, evidenceDetail)
	}

	repairs := repairFailures(task)
	repairsOK := repairs < 3
	setCheck(checks, "ci_repairs", repairsOK, fmt.Sprintf("%d recorded CI repair failures", repairs))
	if !repairsOK {
		reasons = append(reasons, "CI still failing after 3 repair attempts")
	}

	high := authorized && closurePass && reviewPass && pipePass && evidencePass && repairsOK
	confidence := "human-gate"
	if high {
		confidence = "high"
	}
	result := ordjson.NewObject()
	result.Set("task", taskID)
	result.Set("repository", repo)
	result.Set("confidence", confidence)
	result.Set("checks", checks)
	result.Set("reasons", reasons)
	result.Set("authorized_merge", AuthorizedMerge)
	if high {
		result.Set("note", "High-confidence merge is allowed for this candidate. `factory merge` still requires the coordinator pane.")
	} else {
		result.Set("note", "Leave a human gate. Do not merge.")
	}
	return result, nil
}

func Merge(s *store.Store, runtimeRoot, taskID string) (*ordjson.Object, error) {
	check, err := MergeCheck(s, taskID)
	if err != nil {
		return nil, err
	}
	if asString(get(check, "confidence")) != "high" {
		return nil, fmt.Errorf("Refusing merge: confidence is %s. %s", asString(get(check, "confidence")), asString(get(check, "note")))
	}
	task, err := s.ReadTask(taskID)
	if err != nil {
		return nil, err
	}
	repo := authorizedRepo(task)
	if occupyingLane(s, taskID, repo) == nil {
		return nil, fmt.Errorf("Refusing merge: task %s is not occupying a factory lane on %s and does not carry %s from this installation.", taskID, repo, ClaimLabel)
	}
	pr := asObject(get(task, "pr"))
	identity := asObject(get(pr, "identity"))
	number := asInt(get(identity, "number"))
	if number == 0 {
		number = asInt(get(pr, "number"))
	}
	head := asString(get(identity, "head_sha"))
	if number == 0 || repo == "" {
		return nil, fmt.Errorf("PR identity is incomplete; reconcile first")
	}
	gh, err := ghBin(runtimeRoot)
	if err != nil {
		return nil, err
	}
	if err := pipeline.MarkReady(gh, "", repo, number, 0); err != nil {
		return nil, fmt.Errorf("could not mark PR #%d ready for merge: %w", number, err)
	}
	args := []string{"pr", "merge", fmt.Sprint(number), "--repo", repo, "--squash"}
	if head != "" {
		args = append(args, "--match-head-commit", head)
	}
	if _, err := runGh(runtimeRoot, args...); err != nil {
		return nil, err
	}
	result := ordjson.NewObject()
	result.Set("task", taskID)
	result.Set("merged", true)
	result.Set("number", jsonInt(number))
	result.Set("repository", repo)
	result.Set("check", check)
	result.Set("note", "GitHub squash-merge requested. Run cleanup after the merge is observed.")
	return result, nil
}

func setCheck(o *ordjson.Object, name string, pass bool, detail string) {
	row := ordjson.NewObject()
	status := "fail"
	if pass {
		status = "pass"
	}
	row.Set("status", status)
	if detail != "" {
		row.Set("detail", detail)
	}
	o.Set(name, row)
}

func authorizedRepo(task *ordjson.Object) string {
	identity := asObject(get(asObject(get(task, "verification_policy")), "project_identity"))
	if identity != nil {
		owner := asString(get(identity, "owner"))
		repo := asString(get(identity, "repo"))
		if owner != "" && repo != "" {
			return owner + "/" + repo
		}
		if name := asString(get(identity, "name")); name != "" {
			return name
		}
	}
	pr := asObject(get(task, "pr"))
	if repo := asString(get(asObject(get(pr, "identity")), "repository")); repo != "" {
		return repo
	}
	if repo := asString(get(pr, "repository")); repo != "" {
		return repo
	}
	return ""
}

func isAuthorized(repo string) bool {
	for _, allowed := range AuthorizedMerge {
		if repo == allowed {
			return true
		}
	}
	return false
}

func occupyingLane(s *store.Store, taskID, repo string) *ordjson.Object {
	reg, err := Read(s)
	if err != nil {
		return nil
	}
	seen := map[string]bool{}
	names := append([]string{}, repo)
	if projects := projectsOf(reg); projects != nil {
		names = append(names, projects.Keys()...)
	}
	for _, name := range names {
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		rec := projectRecord(reg, name)
		for _, lane := range lanes(rec) {
			if asString(get(lane, "task")) == taskID {
				return lane
			}
		}
	}
	return nil
}

func reviewApproved(task *ordjson.Object) bool {
	head := evidenceview.CurrentCandidate(task)
	if head == "" {
		return false
	}
	raw, _ := task.Get("evidence")
	list, _ := raw.([]any)
	for i := len(list) - 1; i >= 0; i-- {
		row := asObject(list[i])
		if asString(get(row, "kind")) != "review" {
			continue
		}
		if asString(get(row, "candidate")) != head {
			continue
		}
		verdict := strings.ToLower(asString(get(row, "verdict")))
		if verdict == "" {
			verdict = strings.ToLower(asString(get(row, "result")))
		}
		return verdict == "approve"
	}
	return false
}

func pipelineGatesOK(s *store.Store, taskID string) (bool, string) {
	record, err := pipeline.Load(s, taskID)
	if err != nil {
		return false, err.Error()
	}
	return pipeline.GatesSettled(record)
}

func evidenceOK(task *ordjson.Object) (bool, string) {
	raw, _ := task.Get("evidence")
	list, _ := raw.([]any)
	var handoff *ordjson.Object
	for i := len(list) - 1; i >= 0; i-- {
		row := asObject(list[i])
		if asString(get(row, "kind")) == "handoff" {
			handoff = row
			break
		}
	}
	if handoff == nil {
		return false, "no worker handoff; factory mode requires before/after evidence"
	}
	artifacts := anyStrings(get(handoff, "artifacts"))
	if nested := asObject(get(handoff, "handoff")); nested != nil {
		artifacts = append(artifacts, anyStrings(get(nested, "artifacts"))...)
	}
	worktree := asString(get(task, "worktree"))
	head := evidenceview.CurrentCandidate(task)
	found := false
	for _, artifact := range artifacts {
		if filepath.Base(artifact) != "comparison.json" {
			continue
		}
		found = true
		resolved, err := resolveComparison(worktree, artifact)
		if err != nil {
			return false, err.Error()
		}
		if err := comparisonPassing(resolved, head); err != nil {
			return false, err.Error()
		}
	}
	if !found {
		return false, "handoff lists no comparison.json; factory mode requires before/after evidence"
	}
	return true, "comparison.json exists in the checkout with a passing verdict for this candidate"
}

func resolveComparison(worktree, artifact string) (string, error) {
	resolved := artifact
	if !filepath.IsAbs(resolved) {
		if worktree == "" {
			return "", fmt.Errorf("comparison.json %s is relative and the task has no worktree", artifact)
		}
		resolved = filepath.Join(worktree, resolved)
	}
	resolved = filepath.Clean(resolved)
	roots := []string{worktree}
	if worktree != "" {
		roots = append(roots, filepath.Join(worktree, ".artifacts", "evidence"))
	}
	if !pathContained(resolved, roots) {
		return "", fmt.Errorf("handoff artifact %s is outside the task checkout and the evidence root", artifact)
	}
	info, err := os.Lstat(resolved)
	if err != nil {
		return "", fmt.Errorf("comparison.json %s does not exist", artifact)
	}
	if info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("comparison.json %s is not a regular file", artifact)
	}
	return resolved, nil
}

func pathContained(path string, roots []string) bool {
	for _, root := range roots {
		if root == "" {
			continue
		}
		rel, err := filepath.Rel(filepath.Clean(root), path)
		if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

var authorizingVerdicts = map[string]bool{
	"red-green": true, "before-after": true,
}

func comparisonPassing(path, head string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("cannot read %s: %w", path, err)
	}
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		return fmt.Errorf("%s is not comparison JSON: %w", path, err)
	}
	verdict := strings.ToLower(fmt.Sprint(zero(payload["verdict"])))
	if !authorizingVerdicts[verdict] {
		return fmt.Errorf("%s verdict %s does not authorize factory merge (need red-green or before-after)", path, verdict)
	}
	sha := comparisonSHA(payload)
	if sha == "" {
		return fmt.Errorf("%s has no candidate SHA; factory merge requires a comparison bound to this candidate", path)
	}
	if head != "" && sha != head {
		return fmt.Errorf("%s is bound to %s, not the current candidate %s", path, sha, head)
	}
	return nil
}

func comparisonSHA(payload map[string]any) string {
	if s, ok := payload["candidate"].(string); ok && s != "" {
		return s
	}
	if obj, ok := payload["candidate"].(map[string]any); ok {
		if s, ok := obj["sha"].(string); ok {
			return s
		}
	}
	if obj, ok := payload["after"].(map[string]any); ok {
		if s, ok := obj["sha"].(string); ok {
			return s
		}
	}
	return ""
}

func repairFailures(task *ordjson.Object) int {
	raw, _ := task.Get("repairs")
	obj := asObject(raw)
	if obj == nil {
		return 0
	}
	ops, _ := obj.Get("operations")
	list, _ := ops.([]any)
	n := 0
	for _, item := range list {
		row := asObject(item)
		if row == nil {
			continue
		}
		key := strings.ToLower(asString(get(row, "key")))
		class := strings.ToLower(asString(get(row, "class")))
		if strings.Contains(key, "ci") || class == "in-scope" && strings.Contains(asString(get(row, "reason")), "CI") {
			n++
		}
	}
	return n
}

func anyStrings(v any) []string {
	list, _ := v.([]any)
	out := make([]string, 0, len(list))
	for _, item := range list {
		if s, ok := item.(string); ok && s != "" {
			out = append(out, s)
		}
	}
	return out
}

func nilIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
