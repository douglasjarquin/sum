package skilltest

import "path/filepath"

func (v *verifyLab) scaffold(repo string, args ...string) (int, map[string]any, string) {
	script := filepath.Join(v.toolkit, "create-verification/scripts/verify_scaffold.py")
	code, rec, _, stderr := runPy(v.t, v.env, script, v.stop, append([]string{"--root", repo, "--json"}, args...)...)
	return code, rec, stderr
}
