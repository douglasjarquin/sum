package prcmd

import (
	"strings"
	"testing"

	"github.com/douglasjarquin/sum/go/internal/ordjson"
)

func TestPublicationWithoutReconciledPRRefuses(t *testing.T) {
	for _, task := range []*ordjson.Object{nil, ordjson.NewObject()} {
		if _, err := destination(task); err == nil || !strings.Contains(err.Error(), "reconciled PR") {
			t.Fatalf("missing destination must refuse before publication: %v", err)
		}
	}
}
