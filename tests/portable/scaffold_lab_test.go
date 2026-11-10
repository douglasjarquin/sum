package skilltest

func (v *verifyLab) scaffold(repo string, args ...string) (int, map[string]any, string) {
	code, rec, _, stderr := runTool(v.t, v.env, v.stop, append([]string{"scaffold", "--root", repo, "--json"}, args...)...)
	return code, rec, stderr
}
