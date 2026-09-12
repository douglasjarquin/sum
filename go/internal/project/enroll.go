package project

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/douglasjarquin/sum/go/internal/app"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/store"
)

type EnrollArgs struct {
	Spec   string
	Host   string
	Remote string
	Path   string
}

func ParseSpec(spec, host string) (*Identity, error) {
	text := strings.TrimSpace(spec)
	if text == "" {
		return nil, fmt.Errorf("Give a repository as owner/repo or a full remote URL.")
	}
	if strings.Contains(text, "://") || strings.Contains(text, "@") {
		identity := ParseRemoteIdentity(text)
		if identity == nil {
			return nil, fmt.Errorf("Cannot read host/owner/repo from %q; pass owner/repo with --host and --remote for an unusual remote.", text)
		}
		if host != "" {
			normalized, err := NormalizeHost(host)
			if err != nil {
				return nil, err
			}
			if normalized != identity.Host {
				return nil, fmt.Errorf("--host %s contradicts the URL host %s.", host, identity.Host)
			}
		}
		return identity, nil
	}
	parts := []string{}
	for _, p := range strings.Split(strings.Trim(text, "/"), "/") {
		if p != "" {
			parts = append(parts, p)
		}
	}
	if len(parts) == 2 {
		h := host
		if h == "" {
			h = DefaultGitHost
		}
		return ProjectIdentity(h, parts[0], parts[1])
	}
	if len(parts) == 3 && host == "" {
		return ProjectIdentity(parts[0], parts[1], parts[2])
	}
	return nil, fmt.Errorf("Repository %q must be owner/repo (optionally --host HOST) or host/owner/repo; a bare name is never expanded to a guessed project.", text)
}

func derivedRemote(identity *Identity) string {
	return fmt.Sprintf("https://%s/%s/%s.git", identity.Host, identity.Owner, identity.Repo)
}

func canonicalPath(root string, identity *Identity) string {
	base := projectsRoot(root)
	if identity.Host != DefaultGitHost {
		base = filepath.Join(base, identity.Host)
	}
	return filepath.Join(base, identity.Owner, identity.Repo)
}

func writeProjects(s *store.Store, value *ordjson.Object) error {
	return ordjson.WriteFile(filepath.Join(s.Home, ProjectsFile), value)
}

func lookup(registry *ordjson.Object, identity *Identity) *ordjson.Object {
	projects := asObject(getPath(registry, "projects"))
	if projects == nil {
		return nil
	}
	return asObject(getPath(projects, identity.Name))
}

func Enroll(s *store.Store, ctx *ordjson.Object, runtimeRoot string, args EnrollArgs) (*ordjson.Object, error) {
	if err := app.RequireCoordinator(s, ctx); err != nil {
		return nil, err
	}
	root, err := InstallationRootLike(s, runtimeRoot)
	if err != nil {
		return nil, err
	}
	identity, err := ParseSpec(args.Spec, args.Host)
	if err != nil {
		return nil, err
	}
	remote := strings.TrimSpace(args.Remote)
	if remote != "" && ParseRemoteIdentity(remote) == nil && !strings.HasPrefix(remote, "file://") && !strings.HasPrefix(remote, "/") && !strings.HasPrefix(remote, "ssh://") && !strings.HasPrefix(remote, "git@") && !strings.Contains(remote, "://") {
		return nil, fmt.Errorf("--remote %q is not a URL or an absolute path.", remote)
	}
	expected := remote
	if expected == "" {
		expected = derivedRemote(identity)
	}
	registry, err := ReadProjects(s)
	if err != nil {
		return nil, err
	}
	if existing := lookup(registry, identity); existing != nil {
		if args.Path != "" {
			supplied, _ := filepath.Abs(args.Path)
			recorded, _ := resolvePath(asString(getPath(existing, "path")))
			if supplied != recorded {
				return nil, fmt.Errorf("%s is already enrolled at %s; a second path is not adopted. Inspect with `project show`.", identity.Name, asString(getPath(existing, "path")))
			}
		}
		if remote != "" && !SameRemote(asString(getPath(existing, "remote")), remote, existing) {
			return nil, fmt.Errorf("%s is enrolled with remote %s; a different remote is refused, not switched.", identity.Name, asString(getPath(existing, "remote")))
		}
		result := ordjson.NewObject()
		result.Set("enrolled", false)
		result.Set("reason", "already-enrolled")
		result.Set("project", ProjectSummary(existing, ObserveProject(existing)))
		result.Set("registry", filepath.Join(s.Home, ProjectsFile))
		return result, nil
	}
	canonical := canonicalPath(root, identity)
	if args.Path != "" {
		return adoptPath(s, ctx, root, identity, expected, args.Path, canonical, "")
	}
	if info, statErr := os.Stat(canonical); statErr == nil && info.IsDir() && !isSymlink(canonical) {
		if top, ok := gitToplevel(canonical); ok {
			resolved, _ := resolvePath(canonical)
			if top == resolved {
				return adoptPath(s, ctx, root, identity, expected, canonical, canonical, "managed")
			}
		}
	}
	if err := os.MkdirAll(filepath.Dir(canonical), 0o700); err != nil {
		return nil, err
	}
	staging := canonical + ".staging"
	_ = os.RemoveAll(staging)
	cloneURL := expected
	if _, err := runGit("clone", "--", cloneURL, staging); err != nil {
		_ = os.RemoveAll(staging)
		return nil, err
	}
	observed, _ := gitRemote(staging, "origin")
	if !SameRemote(expected, observed, identityObject(identity)) {
		_ = os.RemoveAll(staging)
		return nil, fmt.Errorf("Cloned origin %q does not match the requested repository %s; the staging clone was removed.", observed, expected)
	}
	if err := os.Rename(staging, canonical); err != nil {
		_ = os.RemoveAll(staging)
		return nil, err
	}
	record := enrollmentRecord(identity, "managed", canonical, observed, canonical, ctx, nil)
	return finishEnrollment(s, record, "cloned")
}

func identityObject(identity *Identity) *ordjson.Object {
	o := ordjson.NewObject()
	o.Set("host", identity.Host)
	o.Set("owner", identity.Owner)
	o.Set("repo", identity.Repo)
	o.Set("name", identity.Name)
	return o
}

func enrollmentRecord(identity *Identity, kind, path, remote, canonical string, ctx *ordjson.Object, note any) *ordjson.Object {
	record := ordjson.NewObject()
	record.Set("name", identity.Name)
	record.Set("host", identity.Host)
	record.Set("owner", identity.Owner)
	record.Set("repo", identity.Repo)
	record.Set("kind", kind)
	record.Set("path", path)
	record.Set("remote", remote)
	record.Set("canonical_path", canonical)
	record.Set("enrolled_at", store.Now())
	by := ordjson.NewObject()
	for _, k := range []string{"machine", "session", "pane"} {
		v, _ := ctx.Get(k)
		by.Set(k, v)
	}
	record.Set("enrolled_by", by)
	record.Set("note", note)
	return record
}

func adoptPath(s *store.Store, ctx *ordjson.Object, root string, identity *Identity, expected, supplied, canonical, kind string) (*ordjson.Object, error) {
	if isSymlink(supplied) {
		return nil, fmt.Errorf("Refusing %s: a symlinked project path could alias another checkout.", supplied)
	}
	info, err := os.Stat(supplied)
	if err != nil || !info.IsDir() {
		return nil, fmt.Errorf("%s is not a directory; nothing was created.", supplied)
	}
	resolved, err := resolvePath(supplied)
	if err != nil {
		resolved = supplied
	}
	top, ok := gitToplevel(resolved)
	if !ok || top != resolved {
		detail := ""
		if ok {
			detail = " (top level: " + top + ")"
		}
		return nil, fmt.Errorf("%s is not the top level of a Git checkout%s; nothing was changed.", resolved, detail)
	}
	remote, _ := gitRemote(resolved, "origin")
	if !SameRemote(expected, remote, identityObject(identity)) {
		return nil, fmt.Errorf("%s has origin %q, not %s; refusing to register a different repository under %s.", resolved, remote, expected, identity.Name)
	}
	if kind == "" {
		resolvedRoot, _ := resolvePath(root)
		if resolved == resolvedRoot {
			kind = "installation"
		} else if resolved == func() string { p, _ := resolvePath(canonical); return p }() {
			kind = "managed"
		} else if strings.HasPrefix(resolved, s.Home) {
			kind = "legacy"
		} else {
			kind = "external"
		}
	}
	record := enrollmentRecord(identity, kind, resolved, remote, canonical, ctx, nil)
	return finishEnrollment(s, record, "adopted-"+kind)
}

func finishEnrollment(s *store.Store, record *ordjson.Object, reason string) (*ordjson.Object, error) {
	unlock, err := s.Lock()
	if err != nil {
		return nil, err
	}
	defer unlock()
	registry, err := ReadProjects(s)
	if err != nil {
		return nil, err
	}
	name := asString(getPath(record, "name"))
	projects := asObject(getPath(registry, "projects"))
	if existing := asObject(getPath(projects, name)); existing != nil {
		result := ordjson.NewObject()
		result.Set("enrolled", false)
		result.Set("reason", "already-enrolled")
		result.Set("project", ProjectSummary(existing, ObserveProject(existing)))
		result.Set("registry", filepath.Join(s.Home, ProjectsFile))
		return result, nil
	}
	projects.Set(name, record)
	if err := writeProjects(s, registry); err != nil {
		return nil, err
	}
	result := ordjson.NewObject()
	result.Set("enrolled", true)
	result.Set("reason", reason)
	result.Set("project", ProjectSummary(record, ObserveProject(record)))
	result.Set("registry", filepath.Join(s.Home, ProjectsFile))
	return result, nil
}

func InstallationRootLike(s *store.Store, runtimeRoot string) (string, error) {
	if filepath.Base(s.Home) == ".sum" && s.Designated() {
		root := filepath.Dir(s.Home)
		top, ok := gitToplevel(root)
		resolved, _ := resolvePath(root)
		if !ok || top != resolved {
			return "", fmt.Errorf("%s is not the top level of a Git checkout.", root)
		}
		return resolved, nil
	}
	return runtimeRoot, nil
}

func Migrate(s *store.Store, ctx *ordjson.Object, runtimeRoot, name string, apply bool) (*ordjson.Object, error) {
	registry, err := ReadProjects(s)
	if err != nil {
		return nil, err
	}
	record := asObject(getPath(asObject(getPath(registry, "projects")), name))
	if record == nil {
		return nil, fmt.Errorf("No enrolled project %q.", name)
	}
	canonical := asString(getPath(record, "canonical_path"))
	observed := ObserveProject(record)
	result := ordjson.NewObject()
	result.Set("name", name)
	result.Set("kind", asString(getPath(record, "kind")))
	result.Set("path", asString(getPath(record, "path")))
	result.Set("canonical_path", canonical)
	result.Set("observed", observed)
	result.Set("apply", apply)
	if asString(getPath(record, "kind")) == "installation" {
		result.Set("blockers", []any{"the installation itself is never migrated"})
		return result, nil
	}
	if apply {
		if err := app.RequireCoordinator(s, ctx); err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("project migrate --apply is refused while references may remain; inspect first and move the clone yourself when the inspection is clean.")
	}
	result.Set("blockers", []any{})
	result.Set("note", "Inspection only. --apply renames a proven-idle clone to the canonical path.")
	return result, nil
}
