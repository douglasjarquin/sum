package inboxview

import (
	"bufio"
	"fmt"
	"io"
	"strings"
)

// Scope names what a view shows. A different scope never shares rows, selection, or expansion with the old one.
type Scope struct {
	Home    string
	Project string
}

type Action int

const (
	ActionNone Action = iota
	ActionRefresh
	ActionQuit
)

// View is view-local state over one Overview. It performs no I/O; Run supplies the reads.
type View struct {
	scope       Scope
	overview    Overview
	loaded      bool
	selected    string
	unavailable bool
	expanded    map[string]bool
	detail      bool
	offset      int
	message     string
	lastError   string
	stale       bool
}

type line struct {
	text     string
	identity string
}

func NewView(scope Scope, overview Overview) *View {
	v := &View{scope: scope}
	v.Refresh(overview, nil)
	return v
}

func (v *View) Selected() string { return v.selected }
func (v *View) Stale() bool      { return v.stale }
func (v *View) InDetail() bool   { return v.detail }
func (v *View) Offset() int      { return v.offset }
func (v *View) Unavailable() bool {
	return v.unavailable
}

// Refresh replaces the rows. On error the prior rows stay, the view is stale, and no route is shown as actionable.
func (v *View) Refresh(overview Overview, err error) {
	if err != nil {
		v.stale = true
		v.lastError = err.Error()
		return
	}
	v.stale, v.lastError = false, ""
	v.overview = overview
	v.loaded = true
	if v.expanded == nil {
		v.expanded = map[string]bool{}
	}
	for _, group := range v.groups() {
		if _, seen := v.expanded[group.Key]; !seen {
			v.expanded[group.Key] = !group.Healthy
		}
	}
	if v.selected == "" {
		v.selectFirst()
		return
	}
	// Presence is decided by the data, not the visible line list, so a row restored by a later refresh clears the flag
	// and a task hidden under a collapsed group is never reported as gone.
	v.unavailable = !v.present(v.selected)
}

// present reports whether identity names a row of the current overview: a visible line, or a task hidden by a
// collapsed group.
func (v *View) present(identity string) bool {
	if identity == "" {
		return false
	}
	if v.find(identity) >= 0 {
		return true
	}
	for _, group := range v.groups() {
		for _, task := range group.Tasks {
			if task.ID == identity {
				return true
			}
		}
	}
	return false
}

// SetScope keeps state for the same scope and starts empty for a different one.
func (v *View) SetScope(scope Scope) {
	if scope == v.scope {
		return
	}
	*v = View{scope: scope}
}

func (v *View) groups() []Group {
	groups := v.overview.Groups
	if v.overview.Standalone != nil {
		groups = append(append([]Group{}, groups...), *v.overview.Standalone)
	}
	return groups
}

func groupLabel(key string) string {
	if key == "" {
		return "(standalone)"
	}
	return key
}

func (v *View) selectable() []string {
	ids := []string{}
	for _, l := range v.listBody() {
		if l.identity != "" {
			ids = append(ids, l.identity)
		}
	}
	return ids
}

func (v *View) find(identity string) int {
	for i, id := range v.selectable() {
		if id == identity {
			return i
		}
	}
	return -1
}

func (v *View) selectFirst() {
	v.unavailable = false
	v.selected = ""
	if ids := v.selectable(); len(ids) > 0 {
		v.selected = ids[0]
	}
}

func (v *View) selectedTask() (TaskRow, Group, bool) {
	id := v.selected
	if strings.HasPrefix(id, "needs-you:") {
		for _, row := range v.overview.NeedsYou {
			if "needs-you:"+row.Identity == id {
				id = row.Task
			}
		}
	}
	for _, group := range v.groups() {
		for _, task := range group.Tasks {
			if task.ID == id {
				return task, group, true
			}
		}
	}
	return TaskRow{}, Group{}, false
}

func (v *View) selectedGroup() string {
	if strings.HasPrefix(v.selected, "project:") {
		return strings.TrimPrefix(v.selected, "project:")
	}
	if _, group, ok := v.selectedTask(); ok {
		return group.Key
	}
	return ""
}

// Command applies one line-oriented command and reports whether the caller must reload or quit.
func (v *View) Command(cmd string) Action {
	v.message = ""
	switch strings.TrimSpace(cmd) {
	case "":
	case "n", "p":
		v.move(cmd == "n")
	case "o":
		if v.unavailable {
			v.message = "selected row is no longer present; b to go back"
			break
		}
		if key := v.selectedGroup(); v.selected != "" && !v.detail {
			v.expanded[key] = !v.expanded[key]
			if !v.expanded[key] {
				// Collapsing hides the task rows, so the selection moves to the group instead of vanishing.
				v.selected = "project:" + key
			}
		}
	case "d":
		if v.unavailable {
			v.message = "selected row is no longer present; b to go back"
			break
		}
		if _, _, ok := v.selectedTask(); ok {
			v.detail, v.offset = true, 0
		} else {
			v.message = "select a task for detail"
		}
	case "b":
		if v.unavailable {
			v.selectFirst()
		}
		v.detail, v.offset = false, 0
	case "r":
		return ActionRefresh
	case "c":
		for _, group := range v.groups() {
			if group.Healthy {
				v.expanded[group.Key] = false
			}
		}
		// Collapsing hides the task rows, so a selected task in a healthy group moves to its group row instead of
		// vanishing, as with o.
		if task, group, ok := v.selectedTask(); ok && !v.detail && v.selected == task.ID && !v.expanded[group.Key] {
			v.selected = "project:" + group.Key
		}
	case "q":
		return ActionQuit
	default:
		v.message = fmt.Sprintf("unknown command %q", strings.TrimSpace(cmd))
	}
	return ActionNone
}

func (v *View) move(forward bool) {
	if v.detail {
		if forward {
			v.offset++
		} else if v.offset > 0 {
			v.offset--
		}
		return
	}
	if v.unavailable {
		v.message = "selected row is no longer present; b to go back"
		return
	}
	ids := v.selectable()
	if len(ids) == 0 {
		return
	}
	i := v.find(v.selected)
	switch {
	case i < 0:
		i = 0
	case forward && i+1 < len(ids):
		i++
	case !forward && i > 0:
		i--
	}
	v.selected = ids[i]
}

func plural(n int, word string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", word)
	}
	return fmt.Sprintf("%d %ss", n, word)
}

func (v *View) header() string {
	c := v.overview.Counts
	text := fmt.Sprintf("SUM  %s | %d inspection | %d coordinator | %d worker", plural(c.Decisions, "decision"), c.Inspection, c.Coordinator, c.Worker)
	if !v.overview.Complete {
		text += fmt.Sprintf(" | coverage unknown (%d sources)", c.UnknownSources)
	}
	return text
}

func (v *View) footer() string {
	return "n/p move  o expand  d detail  b back  r refresh  c collapse-all  q quit"
}

// status is the one line for the last error and the last command's message; empty when there is nothing to say.
func (v *View) status() string {
	parts := []string{}
	if v.stale {
		parts = append(parts, "(stale: "+Sanitize(v.lastError)+")")
	}
	if v.message != "" {
		parts = append(parts, v.message)
	}
	return strings.Join(parts, "  ")
}

func (v *View) body() []line {
	if v.detail {
		return v.detailBody()
	}
	return v.listBody()
}

func (v *View) listBody() []line {
	out := []line{}
	if !v.loaded {
		return append(out, line{text: "no overview loaded (r to refresh)"})
	}
	if v.unavailable {
		out = append(out, line{text: "Selected row " + Sanitize(v.selected) + " is no longer present (r refresh, b back)"})
	}
	out = append(out, line{text: "NEEDS YOU"})
	if len(v.overview.NeedsYou) == 0 {
		out = append(out, line{text: "  nothing waiting on you"})
	}
	for _, row := range v.overview.NeedsYou {
		out = append(out, line{identity: "needs-you:" + row.Identity, text: fmt.Sprintf("%s  %s  %s", groupLabel(Sanitize(row.Project)), Sanitize(row.Task), Sanitize(row.Text.Text))})
	}
	projects := "PROJECTS"
	if v.overview.Project != "" {
		projects += "  focus: " + Sanitize(v.overview.Project)
	}
	out = append(out, line{text: projects})
	for _, group := range v.groups() {
		marker := "+"
		if v.expanded[group.Key] {
			marker = "-"
		}
		c := group.Counts
		summary := fmt.Sprintf("%s %s  %s, %d inspection, %d working, %s", marker, groupLabel(Sanitize(group.Key)), plural(c.Decisions, "decision"), c.Inspection, c.Working, plural(c.Tasks, "task"))
		if group.Healthy {
			summary += ", healthy"
		}
		if len(group.Gaps) > 0 {
			summary += fmt.Sprintf(", %d unreadable", len(group.Gaps))
		}
		if group.Factory != nil {
			summary += fmt.Sprintf(", factory %s", plural(len(group.Factory.Lanes), "lane"))
		}
		out = append(out, line{identity: "project:" + group.Key, text: summary})
		if !v.expanded[group.Key] {
			continue
		}
		for _, task := range group.Tasks {
			out = append(out, line{identity: task.ID, text: "  " + taskLine(task)})
		}
		if group.Factory != nil {
			for _, lane := range group.Factory.Lanes {
				out = append(out, line{text: fmt.Sprintf("    lane issue %s  %s  %s  claimed %s", Sanitize(lane.Issue), Sanitize(lane.Task), Sanitize(lane.State), Sanitize(lane.ClaimedAt))})
			}
		}
	}
	if len(v.overview.Gaps) > 0 {
		out = append(out, line{text: fmt.Sprintf("UNREADABLE  %s outside any project", plural(len(v.overview.Gaps), "source"))})
	}
	out = append(out, line{text: fmt.Sprintf("COORDINATOR  %d items, %d inspection, across %s", v.overview.Coordinator.Items, v.overview.Coordinator.Inspection, plural(v.overview.Coordinator.Tasks, "task"))})
	return out
}

func taskLine(task TaskRow) string {
	parts := []string{Sanitize(task.ID), Sanitize(task.State), Sanitize(task.Stage)}
	if task.Branch != "" {
		parts = append(parts, Sanitize(task.Branch))
	}
	if task.Decisions > 0 {
		parts = append(parts, plural(task.Decisions, "decision"))
	}
	if task.Observed != nil {
		parts = append(parts, "observed "+Sanitize(task.Observed.Status))
	}
	if task.Archived {
		parts = append(parts, "archived")
	}
	return strings.Join(parts, "  ")
}

func (v *View) detailBody() []line {
	task, group, ok := v.selectedTask()
	if !ok {
		return []line{{text: "Selected row " + Sanitize(v.selected) + " is no longer present (r refresh, b back)"}}
	}
	out := []line{{text: "TASK " + Sanitize(task.ID) + "  project " + groupLabel(Sanitize(group.Key))}}
	out = append(out, line{text: fmt.Sprintf("  state %s  stage %s  status %s", Sanitize(task.State), Sanitize(task.Stage), Sanitize(task.Status))})
	if task.Repository != "" || task.Branch != "" {
		out = append(out, line{text: "  repository " + Sanitize(task.Repository) + "  branch " + Sanitize(task.Branch)})
	}
	if task.Observed != nil {
		out = append(out, line{text: "  saved observation: " + Sanitize(task.Observed.Status) + " at " + Sanitize(task.Observed.At) + " (a recorded observation, not freshness)"})
	}
	if v.stale {
		out = append(out, line{text: "  stale: routes withheld until a refresh succeeds"})
	} else {
		out = append(out, line{text: "  open: sumctl " + strings.Join(task.Detail, " ")})
	}
	for _, gap := range group.Gaps {
		if gap.TaskID == task.ID {
			out = append(out, line{text: "  unreadable " + Sanitize(gap.Path) + ": " + Sanitize(gap.Reason)})
		}
	}
	for _, item := range task.Items {
		out = append(out, line{text: fmt.Sprintf("  %s  %s %s  %s", Sanitize(string(item.Kind)), Sanitize(string(item.Source.Kind)), Sanitize(item.Source.ID), Sanitize(item.Reason))})
		if item.Text.Text != "" {
			out = append(out, line{text: "    " + Sanitize(item.Text.Text)})
			if item.Text.Truncated {
				out = append(out, line{text: fmt.Sprintf("    (first %d of %d characters; the open route has the rest)", len([]rune(item.Text.Text)), item.Text.Chars)})
			}
		}
		if v.stale {
			continue
		}
		if len(item.Answer) > 0 {
			out = append(out, line{text: "    answer: sumctl " + strings.Join(item.Answer, " ") + " ..."})
		}
		if len(item.Detail) > 0 {
			out = append(out, line{text: "    detail: sumctl " + strings.Join(item.Detail, " ")})
		}
	}
	return out
}

// Render fits the view into height lines of at most width cells. The selected row is always in the window.
func (v *View) Render(width, height int) string {
	if width < 8 {
		width = 8
	}
	if height < 4 {
		height = 4
	}
	body := v.body()
	if v.detail {
		body = wrapLines(body, width)
	}
	status := v.status()
	available := height - 2
	if status != "" {
		available--
	}
	window := len(body)
	notice := ""
	if len(body) > available {
		window = available - 1
	}
	if v.detail {
		if v.offset > len(body)-window {
			v.offset = max(0, len(body)-window)
		}
	} else {
		v.offset = min(v.offset, max(0, len(body)-window))
		for i, l := range body {
			if l.identity == v.selected && v.selected != "" {
				if i < v.offset {
					v.offset = i
				}
				if i >= v.offset+window {
					v.offset = i - window + 1
				}
			}
		}
	}
	end := min(len(body), v.offset+window)
	if len(body) > available {
		notice = fmt.Sprintf("… %d above, %d more rows (n/p to scroll)", v.offset, len(body)-end)
	}
	var out strings.Builder
	out.WriteString(Truncate(v.header(), width) + "\n")
	for _, l := range body[v.offset:end] {
		prefix := "  "
		if l.identity != "" && l.identity == v.selected && !v.detail {
			prefix = "> "
		}
		out.WriteString(Truncate(prefix+l.text, width) + "\n")
	}
	if notice != "" {
		out.WriteString(Truncate(notice, width) + "\n")
	}
	if status != "" {
		out.WriteString(Truncate(status, width) + "\n")
	}
	out.WriteString(Truncate(v.footer(), width) + "\n")
	return out.String()
}

// wrapLines keeps full text reachable in detail mode instead of truncating it.
func wrapLines(body []line, width int) []line {
	out := []line{}
	for _, l := range body {
		text := l.text
		indent := len(text) - len(strings.TrimLeft(text, " "))
		pad := strings.Repeat(" ", min(indent, width/4))
		first := true
		for {
			limit := width - 2
			if !first {
				limit -= len(pad)
			}
			if displayWidth(text) <= limit {
				if first {
					out = append(out, line{text: text})
				} else {
					out = append(out, line{text: pad + text})
				}
				break
			}
			head, tail := splitWidth(text, limit)
			if cut := strings.LastIndex(head, " "); cut > len(head)/2 {
				head, tail = head[:cut], head[cut+1:]+tail
			}
			head = strings.TrimRight(head, " ")
			if first {
				out = append(out, line{text: head})
			} else {
				out = append(out, line{text: pad + head})
			}
			text = strings.TrimLeft(tail, " ")
			first = false
			if text == "" {
				break
			}
		}
	}
	return out
}

func splitWidth(text string, width int) (string, string) {
	total := 0
	for i, r := range text {
		w := runeWidth(r)
		if total+w > width {
			return text[:i], text[i:]
		}
		total += w
	}
	return text, ""
}

// Sanitize removes terminal control characters and escape sequences from saved prose; printable Unicode stays.
func Sanitize(s string) string {
	var b strings.Builder
	runes := []rune(s)
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		switch {
		case r == 0x1b:
			i = skipEscape(runes, i)
		case r == '\t' || r == '\n' || r == '\r' || r == ' ' || r == ' ':
			b.WriteRune(' ')
		case r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f):
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// skipEscape returns the index of the last rune of the sequence starting at runes[i] == ESC.
func skipEscape(runes []rune, i int) int {
	if i+1 >= len(runes) {
		return i
	}
	switch runes[i+1] {
	case '[':
		for j := i + 2; j < len(runes); j++ {
			if runes[j] >= 0x40 && runes[j] <= 0x7e {
				return j
			}
		}
		return len(runes)
	case ']', 'P', '^', '_':
		for j := i + 2; j < len(runes); j++ {
			if runes[j] == 0x07 {
				return j
			}
			if runes[j] == 0x1b && j+1 < len(runes) && runes[j+1] == '\\' {
				return j + 1
			}
		}
		return len(runes)
	}
	return i + 1
}

func runeWidth(r rune) int {
	switch {
	case r < 0x1100:
		return 1
	case r <= 0x115f, r == 0x2329, r == 0x232a,
		r >= 0x2e80 && r <= 0x303e, r >= 0x3041 && r <= 0x33ff,
		r >= 0x3400 && r <= 0x4dbf, r >= 0x4e00 && r <= 0x9fff,
		r >= 0xa000 && r <= 0xa4cf, r >= 0xac00 && r <= 0xd7a3,
		r >= 0xf900 && r <= 0xfaff, r >= 0xfe30 && r <= 0xfe4f,
		r >= 0xff00 && r <= 0xff60, r >= 0xffe0 && r <= 0xffe6,
		r >= 0x1f300 && r <= 0x1f64f, r >= 0x1f900 && r <= 0x1f9ff,
		r >= 0x20000 && r <= 0x3fffd:
		return 2
	}
	return 1
}

func displayWidth(s string) int {
	total := 0
	for _, r := range s {
		total += runeWidth(r)
	}
	return total
}

// Truncate fits text into width cells, ending with an explicit marker when anything was cut.
func Truncate(text string, width int) string {
	if displayWidth(text) <= width {
		return text
	}
	head, _ := splitWidth(text, width-1)
	return head + "…"
}

// Run is the explicit-refresh loop: one read, one render, one line of input, repeat. No polling, no raw mode.
func Run(reader io.Reader, writer io.Writer, width, height int, scope Scope, load func() (Overview, error)) error {
	view := &View{scope: scope}
	view.Refresh(load())
	if _, err := io.WriteString(writer, view.Render(width, height)); err != nil {
		return err
	}
	scanner := bufio.NewScanner(reader)
	for scanner.Scan() {
		switch view.Command(scanner.Text()) {
		case ActionQuit:
			return nil
		case ActionRefresh:
			view.Refresh(load())
		}
		if _, err := io.WriteString(writer, "\n"+view.Render(width, height)); err != nil {
			return err
		}
	}
	return scanner.Err()
}
