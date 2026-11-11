package publish

import (
	"reflect"
	"testing"
)

// The marker, splice, and URL-harvest rules are checked here rather than through the command line because the cases that matter (a CRLF
// body, a malformed marker pair, a hand-written block) cannot be staged byte for byte through the fake GitHub CLI.
func TestMarkersAndSpliceKeepEveryOtherByte(t *testing.T) {
	body := "top\r\n\r\n<!-- before-and-after:start -->\r\nold\r\n<!-- before-and-after:end -->\r\n\r\nbottom\r\n"
	at, err := findBlock(body)
	if err != nil || at == nil {
		t.Fatalf("findBlock: %v %v", at, err)
	}
	got := splice(body, at, "<!-- before-and-after:start -->\nnew\n<!-- before-and-after:end -->\n")
	if want := "top\r\n\r\n<!-- before-and-after:start -->\nnew\n<!-- before-and-after:end -->\r\n\r\nbottom\r\n"; got != want {
		t.Fatalf("splice replaced more than the block: %q", got)
	}
	if got := splice("prose\n", nil, "BLOCK\n"); got != "prose\n\nBLOCK\n" {
		t.Fatalf("splice appended %q", got)
	}
	for _, malformed := range []string{
		"<!-- before-and-after:start -->\nno end",
		"<!-- before-and-after:start -->\n<!-- before-and-after:end -->\n<!-- before-and-after:start -->\n<!-- before-and-after:end -->",
	} {
		if _, err := findBlock(malformed); err == nil {
			t.Fatalf("findBlock accepted %q", malformed)
		} else if _, isRefused := err.(refused); !isRefused {
			t.Fatalf("findBlock(%q) failed with %T, want a refusal", malformed, err)
		}
	}
	harvested := harvestURLs("<!-- sum-media: before=aaa after=bbb -->\n| ![b](https://github.com/user-attachments/assets/1) | ![a](https://github.com/user-attachments/assets/2) |\n" +
		"<!-- sum-media: after=ccc -->\nhttps://github.com/user-attachments/assets/3\n")
	want := map[string]string{"aaa": "https://github.com/user-attachments/assets/1", "bbb": "https://github.com/user-attachments/assets/2", "ccc": "https://github.com/user-attachments/assets/3"}
	if !reflect.DeepEqual(harvested, want) {
		t.Fatalf("harvestURLs: %v", harvested)
	}
}
