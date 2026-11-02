package release

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"unicode/utf8"

	"github.com/douglasjarquin/sum/go/internal/contract"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
)

func RepairTemplateContract(root, sha string) (*ordjson.Object, error) {
	if !sha40Hex.MatchString(sha) {
		return nil, fmt.Errorf("give the full staged release commit SHA")
	}
	path := filepath.Join(releasesDir(root), sha)
	for _, member := range []string{filepath.Join(root, ".local"), releasesDir(root), path, filepath.Join(path, Manifest)} {
		if info, err := os.Lstat(member); err != nil || info.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("release contract repair requires its own directory and manifest: %s", member)
		}
	}
	manifestPath := filepath.Join(path, Manifest)
	original, err := os.ReadFile(manifestPath)
	if err != nil {
		return nil, err
	}
	if !utf8.Valid(original) {
		return nil, fmt.Errorf("release manifest must be valid UTF-8 before its bytes can be archived")
	}
	manifest, err := verifyReleaseContents(path, sha)
	if err != nil {
		return nil, err
	}
	result := ordjson.NewObject()
	result.Set("sha", sha)
	result.Set("path", path)
	result.Set("changed", false)
	if verifyManifestCapabilities(path, manifest) == nil {
		result.Set("note", "Release capabilities already match its verified helper; nothing changed.")
		return result, nil
	}
	supports := asObject(getPath(manifest, "supports"))
	if supports == nil {
		return nil, fmt.Errorf("release has no supports object; only an omitted HC template contract can be repaired")
	}
	if _, exists := supports.Get("hc_template_schema"); exists {
		return nil, fmt.Errorf("release already records HC template schemas; a differing contract cannot be repaired")
	}
	offered, err := ReadTargetContract(context.Background(), path)
	if err != nil {
		return nil, err
	}
	var recorded contract.Release
	raw, err := ordjson.MarshalCompact(manifest)
	if err != nil {
		return nil, err
	}
	if err = json.Unmarshal(raw, &recorded); err != nil {
		return nil, err
	}
	recorded.Supports.HCTemplateSchema = offered.Supports.HCTemplateSchema
	if !reflect.DeepEqual(offered, recorded) {
		return nil, fmt.Errorf("release differs from its helper beyond the omitted HC template schemas; repair refused")
	}
	values := make([]any, len(offered.Supports.HCTemplateSchema))
	for i, schema := range offered.Supports.HCTemplateSchema {
		values[i] = json.Number(strconv.Itoa(schema))
	}
	supports.Set("hc_template_schema", values)
	if err = verifyManifestCapabilities(path, manifest); err != nil {
		return nil, err
	}
	if _, err = verifyReleaseContents(path, sha); err != nil {
		return nil, err
	}
	current, err := os.ReadFile(manifestPath)
	if err != nil || !bytes.Equal(current, original) {
		return nil, fmt.Errorf("release manifest changed during repair; nothing written")
	}
	digest := sha256.Sum256(original)
	backup := filepath.Join(root, ".local", "release-contract-repairs", sha, hex.EncodeToString(digest[:])+".json")
	for _, dir := range []string{filepath.Dir(filepath.Dir(backup)), filepath.Dir(backup)} {
		if err = os.Mkdir(dir, 0700); err != nil && !os.IsExist(err) {
			return nil, err
		}
		info, statErr := os.Lstat(dir)
		if statErr != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("release repair archive directory must not be a symlink: %s", dir)
		}
	}
	archive := ordjson.NewObject()
	archive.Set("sha", sha)
	archive.Set("manifest_sha256", hex.EncodeToString(digest[:]))
	archive.Set("original_manifest", string(original))
	if info, statErr := os.Lstat(backup); statErr == nil && info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("release repair archive must not be a symlink")
	} else if statErr != nil && !os.IsNotExist(statErr) {
		return nil, statErr
	}
	if prior, readErr := ordjson.ReadFile(backup); readErr == nil {
		if asString(getPath(asObject(prior), "original_manifest")) != string(original) {
			return nil, fmt.Errorf("release repair archive already exists with different content")
		}
	} else if !errors.Is(readErr, os.ErrNotExist) {
		return nil, readErr
	} else if err = ordjson.WriteFile(backup, archive); err != nil {
		return nil, err
	}
	if err = ordjson.WriteFile(manifestPath, manifest); err != nil {
		return nil, err
	}
	if _, err = VerifyRelease(path, sha); err != nil {
		return nil, fmt.Errorf("repaired metadata failed verification; original manifest retained at %s: %w", backup, err)
	}
	result.Set("changed", true)
	result.Set("original_manifest", backup)
	result.Set("hc_template_schema", values)
	result.Set("note", "The omitted template contract was regenerated from the checksum-verified helper. Runtime selection, processes and task records were unchanged.")
	return result, nil
}
