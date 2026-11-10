package publish

import (
	"fmt"
	"os/exec"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/douglasjarquin/sum/tools/verify/internal/evidence/proc"
	"github.com/douglasjarquin/sum/tools/verify/internal/evidence/value"
)

var (
	ghVersion   = regexp.MustCompile(`gh version (\d+)\.(\d+)\.(\d+)`)
	ghAttachMin = []int{2, 99, 0}
)

// ghTimeout says a gh call did not finish in time, so whether it changed anything on GitHub is unknown.
type ghTimeout string

func (e ghTimeout) Error() string { return string(e) }

// gh is the GitHub CLI: the only way anything here reaches GitHub, running under whoever invokes the tool.
type gh struct {
	binary  string // "" when gh is not installed.
	timeout int    // Seconds allowed for a call that does not set its own bound.
}

func newGh(binary string, timeout int) *gh {
	if binary == "" {
		binary, _ = exec.LookPath("gh")
	}
	return &gh{binary: binary, timeout: timeout}
}

// ghCall is one gh invocation; the zero bound means the client's default, and check turns a non-zero exit into a refusal.
type ghCall struct {
	args    []string
	dir     string
	timeout int
	check   bool
}

func firstTwo(args []string) string {
	return strings.Join(args[:min(2, len(args))], " ")
}

func (g *gh) run(call ghCall) (proc.Result, error) {
	if g.binary == "" {
		return proc.Result{}, refused("GitHub CLI (gh) is not installed or not on PATH")
	}
	timeout := call.timeout
	if timeout == 0 {
		timeout = g.timeout
	}
	result, err := proc.Run(append([]string{g.binary}, call.args...), proc.Options{Dir: call.dir, Timeout: time.Duration(timeout) * time.Second})
	if err == proc.ErrTimeout {
		second := ""
		if len(call.args) > 1 {
			second = call.args[1]
		}
		return result, ghTimeout(fmt.Sprintf("gh %s %s did not finish within %ds; its effect is unknown", call.args[0], second, timeout))
	}
	if err != nil {
		return result, fmt.Errorf("gh %s could not start: %w", firstTwo(call.args), err)
	}
	if call.check && result.Code != 0 {
		return result, refused(fmt.Sprintf("gh %s exited %d: %s", firstTwo(call.args), result.Code, lastChars(output(result), 600)))
	}
	return result, nil
}

// output is what a failed gh call said: its stderr, or its stdout when stderr is empty.
func output(result proc.Result) string {
	if result.Stderr != "" {
		return strings.TrimSpace(result.Stderr)
	}
	return strings.TrimSpace(result.Stdout)
}

func lastChars(text string, n int) string {
	if runes := []rune(text); len(runes) > n {
		return string(runes[len(runes)-n:])
	}
	return text
}

// json runs a call that must succeed and print one JSON object.
func (g *gh) json(args []string, timeout int) (*value.Object, error) {
	result, err := g.run(ghCall{args: args, timeout: timeout, check: true})
	if err != nil {
		return nil, err
	}
	parsed, err := value.Decode([]byte(result.Stdout))
	if err != nil || value.Obj(parsed) == nil {
		return nil, refused(fmt.Sprintf("gh %s did not return JSON: %s", firstTwo(args), firstChars(result.Stdout, 200)))
	}
	return value.Obj(parsed), nil
}

// capabilities reports what the installed GitHub CLI can do, read from the binary itself; a version number alone never proves `--attach` exists.
func (g *gh) capabilities() *value.Object {
	info := value.NewObject("gh", nil, "version", nil, "attach", false, "authenticated", nil, "reason", nil)
	if g.binary == "" {
		info.Set("reason", "gh is not installed or not on PATH")
		return info
	}
	info.Set("gh", g.binary)
	err := func() error {
		version, err := g.run(ghCall{args: []string{"--version"}, timeout: 20, check: true})
		if err != nil {
			return err
		}
		match := ghVersion.FindStringSubmatch(version.Stdout)
		atLeast := false
		if match != nil {
			info.Set("version", strings.Join(match[1:], "."))
			var parts []int
			for _, text := range match[1:] {
				part, _ := strconv.Atoi(text)
				parts = append(parts, part)
			}
			atLeast = slices.Compare(parts, ghAttachMin) >= 0
		} else {
			firstLine, _, _ := strings.Cut(strings.TrimSpace(version.Stdout), "\n")
			info.Set("version", firstChars(firstLine, 60))
		}
		help, err := g.run(ghCall{args: []string{"pr", "edit", "--help"}, timeout: 20})
		if err != nil {
			return err
		}
		attach := strings.Contains(help.Stdout, "--attach") && atLeast
		info.Set("attach", attach)
		if !attach {
			info.Set("reason", fmt.Sprintf("gh %s has no `pr edit --attach`; GitHub CLI %d.%d.%d+ is required. Local capture and text-only publication work; media publication is deferred.",
				value.Text(info.Get("version")), ghAttachMin[0], ghAttachMin[1], ghAttachMin[2]))
		}
		auth, err := g.run(ghCall{args: []string{"auth", "status"}, timeout: 30})
		if err != nil {
			return err
		}
		info.Set("authenticated", auth.Code == 0)
		return nil
	}()
	if err != nil {
		info.Set("reason", err.Error())
	}
	return info
}
