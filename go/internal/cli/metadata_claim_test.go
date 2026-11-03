package cli

import (
	"bytes"
	"os"
	"testing"

	"github.com/douglasjarquin/sum/go/internal/app"
	"github.com/douglasjarquin/sum/go/internal/metadata"
	"github.com/douglasjarquin/sum/go/internal/store"
	"github.com/spf13/cobra"
)

func TestPostCommandProjectionRefusesTransferredClaim(t *testing.T) {
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	session, err := s.ClaimCoordinator("", "first", false)
	if err != nil {
		t.Fatal(err)
	}
	ctx, err := app.RequireClaim(s, session.Token())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = metadata.Enable(s, ctx, "", false); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(metadata.Path(s))
	if err != nil {
		t.Fatal(err)
	}
	replacement, err := store.Open(s.Root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = replacement.ClaimCoordinator("", "second", true); err != nil {
		t.Fatal(err)
	}
	opts := &rootOptions{home: s.Root, claim: session.Token()}
	cmd := &cobra.Command{Use: "test", Annotations: map[string]string{projectsAnnotation: "all"}}
	opts.projectAfter(cmd, nil)
	after, err := os.ReadFile(metadata.Path(s))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("post-command projection used a transferred claim")
	}
}
