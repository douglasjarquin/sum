package publish

import (
	"fmt"
	"os"

	"github.com/douglasjarquin/sum/tools/verify/internal/tool"
	"github.com/douglasjarquin/sum/tools/verify/internal/value"
)

const eventLimit = 200

// loadReceipts reads the receipt store: one entry per attachment content hash and destination, the blocks this publisher wrote, and a
// bounded log of what each publication attempted. A missing store is an empty one.
func loadReceipts(path string) (*value.Object, error) {
	if info, err := os.Stat(path); err != nil || !info.Mode().IsRegular() {
		return value.NewObject("schema", schema, "attachments", value.NewObject(), "blocks", []any{}, "events", []any{}), nil
	}
	receipts, err := loadJSON(path, "receipts")
	if err != nil {
		return nil, err
	}
	if !value.Equal(receipts.Get("schema"), schema) {
		return nil, refused(fmt.Sprintf("receipts at %s have schema %s, expected %d", path, value.Repr(receipts.Get("schema")), schema))
	}
	return receipts, nil
}

func receiptKey(sha string, destination *value.Object) string {
	return fmt.Sprintf("%s@%s#%s", sha, value.Text(destination.Get("repository")), value.Text(destination.Get("number")))
}

// recordedURL is the attachment URL already recorded for a content hash at this destination, or "".
func recordedURL(receipts *value.Object, sha string, destination *value.Object) string {
	entry := value.Obj(value.Obj(receipts.Get("attachments")).Get(receiptKey(sha, destination)))
	return value.Str(entry.Get("url"))
}

// recordEvent appends to the receipt log, keeping only the most recent events.
func recordEvent(receipts *value.Object, fields ...any) {
	event := value.NewObject("at", tool.UTCNow())
	for i := 0; i+1 < len(fields); i += 2 {
		event.Set(fields[i].(string), fields[i+1])
	}
	events := append(value.List(receipts.Get("events")), event)
	receipts.Set("events", events[max(0, len(events)-eventLimit):])
}
