package evidence

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/douglasjarquin/sum/tools/verify/internal/evidence/args"
	"github.com/douglasjarquin/sum/tools/verify/internal/evidence/proc"
	"github.com/douglasjarquin/sum/tools/verify/internal/evidence/value"
	"github.com/douglasjarquin/sum/tools/verify/skills"
)

const (
	browserDriver   = "evidence_browser.mjs"
	maxSecondsCap   = 60
	maxWidth        = 1920
	maxHeight       = 1080
	defaultMaxBytes = 25 * 1024 * 1024
)

var browserSpec = args.Spec{Options: []args.Option{
	{Name: "--url", Values: 1, Required: true},
	{Name: "--step", Values: 1},
	{Name: "--viewport", Values: 1, Default: "1280x800"},
	{Name: "--theme", Values: 1, Choices: []string{"light", "dark"}, Default: "light"},
	{Name: "--locale", Values: 1, Default: "en-US"},
	{Name: "--timezone", Values: 1, Default: "UTC"},
	{Name: "--expect-text", Values: 1},
	{Name: "--expect-selector", Values: 1},
	{Name: "--observe", Values: 1},
	{Name: "--no-video"},
	{Name: "--max-seconds", Values: 1, Int: true, Default: "20"},
	{Name: "--max-frames", Values: 1, Int: true, Default: "600"},
	{Name: "--step-timeout", Values: 1, Int: true, Default: "15"},
	{Name: "--convert"},
	{Name: "--side-effect", Values: 2},
	{Name: "--side-effect-text", Values: 1},
}}

func parseStep(step string) (*value.Object, error) {
	action, arg, _ := strings.Cut(step, "=")
	switch action {
	case "goto":
		return value.NewObject("action", "goto", "url", arg), nil
	case "click":
		return value.NewObject("action", "click", "selector", arg), nil
	case "type":
		selector, text, _ := strings.Cut(arg, "=")
		return value.NewObject("action", "type", "selector", selector, "text", text), nil
	case "press":
		return value.NewObject("action", "press", "key", arg), nil
	case "wait-text":
		return value.NewObject("action", "wait-text", "text", arg), nil
	case "wait":
		if ms, err := strconv.Atoi(strings.TrimSpace(arg)); err == nil {
			return value.NewObject("action", "wait", "ms", ms), nil
		}
	}
	return nil, usage("--step " + value.Repr(step) + ": unknown action; use goto=, click=, type=SEL=TEXT, press=, wait-text=, wait=MS")
}

func parseViewport(viewport string) (width, height int, err error) {
	w, h, _ := strings.Cut(strings.ToLower(viewport), "x")
	width, errW := strconv.Atoi(strings.TrimSpace(w))
	height, errH := strconv.Atoi(strings.TrimSpace(h))
	if errW != nil || errH != nil || width > maxWidth || height > maxHeight || width < 200 || height < 200 {
		return 0, 0, usage(fmt.Sprintf("--viewport must be between 200x200 and %dx%d", maxWidth, maxHeight))
	}
	return width, height, nil
}

// runDriver runs the browser driver on one job and returns its exit status, stderr, and the result it printed (empty when it printed none).
// The driver is written from the copy built into this tool to a private directory for the run, so the capture does not depend on where
// the skill sits on disk.
func runDriver(job *value.Object, timeout time.Duration) (code int, stderr string, result *value.Object, err error) {
	source, err := skills.Files.ReadFile("evidence/scripts/" + browserDriver)
	if err != nil {
		return 0, "", nil, err
	}
	private, err := os.MkdirTemp("", "evidence-driver-")
	if err != nil {
		return 0, "", nil, err
	}
	defer os.RemoveAll(private)
	script := filepath.Join(private, browserDriver)
	if err := os.WriteFile(script, source, 0o600); err != nil {
		return 0, "", nil, err
	}
	input, err := json.Marshal(job)
	if err != nil {
		return 0, "", nil, err
	}
	node, _ := exec.LookPath("node")
	ran, err := proc.Run([]string{node, script}, proc.Options{Stdin: strings.NewReader(string(input)), Timeout: timeout})
	if err == proc.ErrTimeout {
		return 0, "", nil, blocked("browser driver exceeded its time bound; diagnostics retained")
	}
	if err != nil {
		return 0, "", nil, err
	}
	result = value.NewObject()
	if printed := strings.TrimSpace(ran.Stdout); printed != "" {
		lines := strings.Split(printed, "\n")
		if parsed, err := value.Decode([]byte(lines[len(lines)-1])); err == nil && value.Obj(parsed) != nil {
			result = value.Obj(parsed)
		}
	}
	return ran.Code, ran.Stderr, result, nil
}

func (c *capture) browser(opts args.Parsed) ([]any, error) {
	record := c.record
	caps := value.Obj(capabilities().Get("browser"))
	viewport, theme, observe := opts.String("--viewport"), opts.String("--theme"), opts.Optional("--observe")
	environment := value.Obj(record.Get("environment"))
	environment.Set("viewport", viewport)
	environment.Set("theme", theme)
	environment.Set("locale", opts.String("--locale"))
	environment.Set("timezone", opts.String("--timezone"))
	environment.Set("browser", caps.Get("binary"))
	if caps.Get("supported") != true {
		return nil, blocked("browser recipe unavailable: " + value.Text(caps.Get("reason")))
	}
	width, height, err := parseViewport(viewport)
	if err != nil {
		return nil, err
	}
	seconds := min(opts.Int("--max-seconds"), maxSecondsCap)
	stepTimeout := opts.Int("--step-timeout")
	steps := []any{value.NewObject("action", "goto", "url", opts.String("--url"))}
	for _, step := range opts.Strings("--step") {
		parsed, err := parseStep(step)
		if err != nil {
			return nil, err
		}
		steps = append(steps, parsed)
	}
	frameDir := filepath.Join(c.dir, "frames")
	if err := os.Mkdir(frameDir, 0o755); err != nil {
		return nil, err
	}
	diagnostics := filepath.Join(c.dir, "browser-diagnostics.log")
	shot := filepath.Join(c.dir, "screenshot.png")
	video := !opts.Bool("--no-video")
	job := value.NewObject("browser", caps.Get("binary"), "viewport", value.NewObject("width", width, "height", height), "locale", opts.String("--locale"),
		"theme", theme, "timezone", opts.String("--timezone"), "steps", steps, "screencast", video, "jpeg_quality", 75, "max_frames", opts.Int("--max-frames"),
		"max_ms", seconds*1000, "max_bytes", defaultMaxBytes, "frame_dir", frameDir, "screenshot", shot, "diagnostics", diagnostics,
		"step_timeout_ms", stepTimeout*1000, "settle_ms", 300, "text_limit", textLimit, "observe_selector", observe)
	record.Set("steps", steps)
	record.Set("diagnostics", filepath.Base(diagnostics))
	exit, stderr, result, err := runDriver(job, time.Duration(seconds+stepTimeout*(len(steps)+2))*time.Second)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(stderr) != "" {
		if log, err := os.OpenFile(diagnostics, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
			_, _ = log.WriteString(tail(stderr, 4000))
			_ = log.Close()
		}
	}
	observations := value.Obj(result.Get("observations"))
	record.Set("driver", value.NewObject("script", browserDriver, "exit", exit, "browser_version", observations.Get("browser_version"), "protocol", observations.Get("protocol_version")))
	environment.Set("browser_version", observations.Get("browser_version"))
	stepResults := result.Get("steps")
	if stepResults == nil {
		stepResults = []any{}
	}
	record.Set("step_results", stepResults)
	console := []any{}
	for _, message := range value.List(result.Get("console")) {
		if len(console) == 50 {
			break
		}
		entry := value.Obj(message).Clone()
		entry.Set("text", c.redact.Redact(value.Str(entry.Get("text"))))
		console = append(console, entry)
	}
	record.Set("console", console)
	text := value.Str(observations.Get("text"))
	var selectorText any
	if observations.Get("selector_text") != nil {
		selectorText = c.redact.Redact(value.Str(observations.Get("selector_text")))
	}
	record.Set("observations", value.NewObject("url", observations.Get("url"), "title", observations.Get("title"), "text", c.redact.Redact(clip(text)),
		"selector", observe, "selector_text", selectorText, "viewport", observations.Get("viewport")))
	if !value.Truthy(result.Get("ok")) {
		failures := []string{fmt.Sprintf("driver exited %d without a result", exit)}
		if reported := value.List(result.Get("errors")); len(reported) > 0 {
			failures = failures[:0]
			for _, failure := range reported {
				failures = append(failures, value.Text(failure))
			}
		}
		return nil, blocked("browser capture failed: " + strings.Join(failures, "; "))
	}
	if seen := value.Obj(observations.Get("viewport")); seen.Len() > 0 {
		if !value.Equal(seen.Get("width"), width) || !value.Equal(seen.Get("height"), height) {
			c.appendTo("limitations", fmt.Sprintf("requested viewport %dx%d but the page saw %sx%s", width, height, value.Text(seen.Get("width")), value.Text(seen.Get("height"))))
		}
		if value.Truthy(seen.Get("dark")) != (theme == "dark") {
			c.appendTo("limitations", fmt.Sprintf("requested theme %s but the page reports dark=%s", theme, value.Text(seen.Get("dark"))))
		}
	}
	for _, expected := range opts.Strings("--expect-text") {
		c.assert("page text contains "+value.Repr(expected), strings.Contains(text, expected))
	}
	for _, pair := range opts.Strings("--expect-selector") {
		selector, expected, _ := strings.Cut(pair, "=")
		observed, isObserved := observations.Get("selector_text").(string)
		if selector != value.Str(observe) || observe == nil {
			isObserved = false
		}
		shown := "(selector not observed; pass the same selector to --observe)"
		if isObserved {
			shown = c.redact.Redact(observed)
		}
		c.assert(fmt.Sprintf("%s text is %s", selector, value.Repr(expected)), isObserved && strings.TrimSpace(observed) == expected, shown)
	}
	if sideEffect := opts.Strings("--side-effect"); len(sideEffect) > 0 {
		method, url := strings.ToUpper(sideEffect[len(sideEffect)-2]), sideEffect[len(sideEffect)-1]
		status, body, failure, err := httpRequest(method, url, nil, 10)
		if err != nil {
			return nil, err
		}
		record.Set("side_effect", value.NewObject("method", method, "url", url, "status", status, "body", c.redact.Redact(clip(body)), "error", failure))
		for _, expected := range opts.Strings("--side-effect-text") {
			c.assert(fmt.Sprintf("side effect %s %s body contains %s", method, url, value.Repr(expected)), strings.Contains(body, expected))
		}
		if failure != nil {
			c.assert(fmt.Sprintf("side effect %s %s answers", method, url), false, failure)
		}
	}
	media := []any{}
	if isFile(shot) {
		info, err := InspectMedia(shot)
		if err != nil {
			return nil, err
		}
		media = append(media, withRole(info, "screenshot"))
	}
	if video {
		recorded, err := c.screencast(frameDir, result, opts.Bool("--convert"))
		if err != nil {
			return nil, err
		}
		media = append(media, recorded...)
	}
	record.Set("frames_dir", "frames")
	return media, nil
}

// screencast assembles the recorded frames into the AVI original and lists it, every frame, and the optional converted copy.
func (c *capture) screencast(frameDir string, result *value.Object, convert bool) ([]any, error) {
	paths, _ := filepath.Glob(filepath.Join(frameDir, "frame-*.jpg"))
	if len(paths) < 1 {
		c.appendTo("limitations", "screencast produced no frames; the recorder did not deliver any (see diagnostics)")
		return nil, nil
	}
	captured := map[string]any{}
	for _, entry := range value.List(result.Get("frames")) {
		captured[value.Str(value.Obj(entry).Get("file"))] = value.Obj(entry).Get("ms")
	}
	frames := make([]frame, len(paths))
	for i, path := range paths {
		ms, ok := value.Number(captured[filepath.Base(path)])
		if !ok {
			ms = float64(i * 100)
		}
		frames[i] = frame{path: path, ms: ms}
	}
	avi := filepath.Join(c.dir, "screencast.avi")
	_, held, err := writeMJPEGAVI(frames, avi)
	if err != nil {
		return nil, err
	}
	info, err := InspectMedia(avi)
	if err != nil {
		return nil, err
	}
	media := []any{withRole(info, "screencast", "source_frames", len(paths), "held_frames", held,
		"encoding", fmt.Sprintf("MJPEG in AVI at %d fps; original JPEG frames under frames/ with their capture times; held repeats are timing only", videoFPS))}
	for _, path := range paths {
		info, err := InspectMedia(path)
		if err != nil {
			return nil, err
		}
		media = append(media, withRole(info, "frame", "ms", captured[filepath.Base(path)]))
	}
	if limit := value.Obj(result.Get("observations")).Get("screencast_limit"); value.Truthy(limit) {
		c.appendTo("limitations", "screencast stopped at bound "+value.Text(limit))
	}
	if !convert {
		return media, nil
	}
	ffmpeg := ffmpegBinary()
	if ffmpeg == "" {
		c.appendTo("limitations", "no working ffmpeg on PATH; original AVI kept, no converted copy")
		return media, nil
	}
	mp4 := filepath.Join(c.dir, "screencast.mp4")
	command := []string{ffmpeg, "-v", "error", "-y", "-i", avi, "-c:v", "libx264", "-pix_fmt", "yuv420p", "-vf", "pad=ceil(iw/2)*2:ceil(ih/2)*2", mp4}
	converted, err := proc.Run(command, proc.Options{})
	if err != nil {
		return nil, err
	}
	if converted.Code != 0 || !isFile(mp4) {
		c.appendTo("limitations", fmt.Sprintf("ffmpeg conversion failed (%d); original AVI kept: %s", converted.Code, head(strings.TrimSpace(converted.Stderr), 200)))
		return media, nil
	}
	producer := "ffmpeg"
	if version, err := proc.Run([]string{ffmpeg, "-version"}, proc.Options{}); err == nil && version.Stdout != "" {
		producer, _, _ = strings.Cut(version.Stdout, "\n")
	}
	info, err = InspectMedia(mp4)
	if err != nil {
		return nil, err
	}
	return append(media, withRole(info, "screencast-converted", "derived", true, "source", filepath.Base(avi), "tool", producer, "command", value.Strings(command[1:]))), nil
}
