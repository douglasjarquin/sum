package evidence

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/douglasjarquin/sum/tools/verify/internal/args"
	"github.com/douglasjarquin/sum/tools/verify/internal/pycompat"
	"github.com/douglasjarquin/sum/tools/verify/internal/tool"
	"github.com/douglasjarquin/sum/tools/verify/internal/value"
)

var inspectSpec = args.Spec{Positionals: []string{"path"}, Options: []args.Option{{Name: "--json"}}}

var promoteSpec = args.Spec{Options: []args.Option{
	{Name: "--run", Values: 1, Required: true},
	{Name: "--to", Values: 1, Required: true},
	{Name: "--json"},
}}

func cmdInspect(argv []string) (int, error) {
	opts, err := args.Parse(inspectSpec, argv)
	if err != nil {
		return 0, err
	}
	path := opts.Positionals[0]
	info, err := InspectMedia(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "inspect: %s invalid: %v\n", path, err)
		return 1, nil
	}
	if opts.Bool("--json") {
		return 0, tool.WriteJSON(os.Stdout, info)
	}
	line := fmt.Sprintf("inspect: %s %s ", path, value.Text(info.Get("type")))
	var facts []string
	for _, key := range []string{"width", "height", "frames", "duration_ms", "bytes"} {
		if info.Has(key) {
			facts = append(facts, key+"="+value.Text(info.Get(key)))
		}
	}
	fmt.Println(line + strings.Join(facts, " "))
	return 0, nil
}

// copyTree copies a directory with the content of anything it links to, keeping each file's mode and modification time.
func copyTree(source, dest string) error {
	info, err := os.Stat(source)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		data, err := os.ReadFile(source)
		if err != nil {
			return err
		}
		if err := os.WriteFile(dest, data, info.Mode().Perm()); err != nil {
			return err
		}
		return os.Chtimes(dest, info.ModTime(), info.ModTime())
	}
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return err
	}
	entries, err := os.ReadDir(source)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if err := copyTree(filepath.Join(source, entry.Name()), filepath.Join(dest, entry.Name())); err != nil {
			return err
		}
	}
	return os.Chmod(dest, info.Mode().Perm())
}

func cmdPromote(argv []string) (int, error) {
	opts, err := args.Parse(promoteSpec, argv)
	if err != nil {
		return 0, err
	}
	root, err := ProjectRoot()
	if err != nil {
		return 0, err
	}
	rootDir, _ := EvidenceRoot(root)
	run := opts.String("--run")
	source := filepath.Join(rootDir, SafeName(run))
	if !pycompat.IsDir(source) {
		return 0, usage(fmt.Sprintf("run %s not found under %s", run, rootDir))
	}
	dest := filepath.Join(pycompat.ResolveExistingPrefix(pycompat.ExpandUser(opts.String("--to"))), SafeName(run))
	if _, err := os.Lstat(dest); err == nil {
		return 0, usage(dest + " already exists; promotion never overwrites")
	}
	if err := copyTree(source, dest); err != nil {
		return 0, err
	}
	verified := 0
	failed := []string{}
	err = filepath.WalkDir(dest, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.Name() != "capture.json" {
			return err
		}
		record, err := readRecord(path)
		if err != nil {
			return err
		}
		bad := verifyHashes(filepath.Dir(path), record)
		for _, name := range bad {
			failed = append(failed, filepath.Base(filepath.Dir(path))+"/"+name)
		}
		verified += value.Obj(record.Get("content_hashes")).Len() - len(bad)
		return nil
	})
	if err != nil {
		return 0, err
	}
	summary := value.NewObject("run", run, "from", source, "to", dest, "files_verified", verified, "hash_failures", value.Strings(failed), "promoted_at", tool.UTCNow())
	if err := writeRecord(filepath.Join(dest, "promoted.json"), summary); err != nil {
		return 0, err
	}
	if opts.Bool("--json") {
		if err := tool.WriteJSON(os.Stdout, summary); err != nil {
			return 0, err
		}
	} else {
		fmt.Printf("promoted: %s verified=%d failures=%d\n", dest, verified, len(failed))
	}
	if len(failed) > 0 {
		return 1, nil
	}
	return 0, nil
}
