package verifycontract

import (
	"encoding/json"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/douglasjarquin/sum/go/internal/contract"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
)

type DispatchMetadata struct {
	Repository  string
	Project     *ordjson.Object
	Launch      *ordjson.Object
	RuntimeRoot string
}

func AddDispatchMetadata(policy *ordjson.Object, metadata DispatchMetadata) {
	policy.Set("repository_path", metadata.Repository)
	policy.Set("project_identity", projectIdentity(metadata.Repository, metadata.Project))
	policy.Set("source_runtime", sourceRuntime(metadata.RuntimeRoot))
	policy.Set("delivery", delivery(metadata.Launch))
}

func projectIdentity(repository string, project *ordjson.Object) *ordjson.Object {
	identity := ordjson.NewObject()
	identity.Set("path", repository)
	for _, key := range []string{"name", "host", "owner", "repo", "kind"} {
		if project == nil {
			continue
		}
		if value, ok := project.Get(key); ok {
			identity.Set(key, value)
		}
	}
	return identity
}

func sourceRuntime(root string) *ordjson.Object {
	result := ordjson.NewObject()
	result.Set("sum_version", contract.SumVersion)
	result.Set("brief_schema", json.Number(strconv.Itoa(contract.BriefSchema)))
	result.Set("runtime_revision", gitRevision(root))
	result.Set("worker_skill_path", "skills/sum-worker/SKILL.md")
	result.Set("worker_skill_sha256", fileHash(root, "skills/sum-worker/SKILL.md"))
	result.Set("reviewer_skill_path", "skills/sum-delivery/SKILL.md")
	result.Set("reviewer_skill_sha256", fileHash(root, "skills/sum-delivery/SKILL.md"))
	rubric := ordjson.NewObject()
	rubric.Set("path", ".agents/skills/verify/references/engineering-principles.md")
	rubric.Set("sha256", fileHash(root, ".agents/skills/verify/references/engineering-principles.md"))
	result.Set("rubric", rubric)
	return result
}

func delivery(launch *ordjson.Object) *ordjson.Object {
	result := ordjson.NewObject()
	result.Set("mode", "herdr-agent")
	tool := ordjson.NewObject()
	if launch != nil {
		for _, key := range []string{"harness", "model", "reasoning", "same_as_root", "preset"} {
			if value, ok := launch.Get(key); ok {
				tool.Set(key, value)
			}
		}
		if source, ok := launch.Get("source"); ok {
			tool.Set("source", source)
		}
	}
	result.Set("tool", tool)
	return result
}

func fileHash(root, relative string) any {
	if root == "" {
		return nil
	}
	data, err := readBounded(filepath.Join(root, filepath.FromSlash(relative)))
	if err != nil {
		return nil
	}
	return sha256Text(string(data))
}

func gitRevision(root string) any {
	if root == "" {
		return nil
	}
	output, err := exec.Command("git", "-C", root, "rev-parse", "HEAD").Output()
	if err != nil {
		return nil
	}
	revision := strings.TrimSpace(string(output))
	if revision == "" {
		return nil
	}
	return revision
}
