package backup

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/douglasjarquin/sum/go/internal/notes"
	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/store"
)

func TestRunIncludesFindingsInRecordsOnlyBackup(t *testing.T) {
	home := t.TempDir()
	s, err := store.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(notes.FindingsPath(s), 0o700); err != nil {
		t.Fatal(err)
	}
	actor := ordjson.NewObject()
	actor.Set("role", "worker")
	identity, _ := json.Marshal(struct {
		TaskID string `json:"task_id"`
		Key    string `json:"key"`
	}{TaskID: "t-aaaaaaaaaaaa", Key: "key"})
	sum := sha256.Sum256(identity)
	record := notes.Record{Schema: notes.Schema, ID: hex.EncodeToString(sum[:]), Key: "key", TaskID: "t-aaaaaaaaaaaa", ProjectID: "team/repo", Author: actor, CreatedAt: store.Now(), Title: "title", Explanation: "explanation", NextStep: "next", References: []string{}, Disposition: "open", LinkState: "none", History: []notes.Event{{Action: "capture", At: store.Now(), Actor: actor}}}
	data, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(notes.FindingsPath(s), record.ID+".json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	if records, gaps := notes.ReadAll(s); len(records) != 1 || len(gaps) != 0 {
		t.Fatalf("records=%#v gaps=%#v", records, gaps)
	}
	destination := filepath.Join(t.TempDir(), "records.tar.gz")
	if _, err := Run(s, destination); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(destination)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	gz, err := gzip.NewReader(file)
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(gz)
	foundRecord := false
	var manifest map[string]any
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		content, err := io.ReadAll(tr)
		if err != nil {
			t.Fatal(err)
		}
		if header.Name == "state/findings/"+record.ID+".json" {
			foundRecord = string(content) == string(data)
		}
		if header.Name == "manifest.json" {
			if err := json.Unmarshal(content, &manifest); err != nil {
				t.Fatal(err)
			}
		}
	}
	if !foundRecord {
		t.Fatal("backup did not preserve the finding record")
	}
	if manifest["finding_records_included"] != float64(1) {
		t.Fatalf("finding_records_included = %v, want 1", manifest["finding_records_included"])
	}
}
