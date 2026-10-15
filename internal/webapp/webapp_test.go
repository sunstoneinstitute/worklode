package webapp

import (
	"io/fs"
	"strings"
	"testing"
)

func TestFSServesPlaceholderIndex(t *testing.T) {
	b, err := fs.ReadFile(FS(), "index.html")
	if err != nil {
		t.Fatalf("ReadFile(index.html) = %v", err)
	}
	if !strings.Contains(string(b), "was not built") {
		t.Errorf("index.html does not contain %q: %s", "was not built", b)
	}
}

func TestBuiltFalseOnCleanCheckout(t *testing.T) {
	if Built() {
		t.Error("Built() = true on a clean checkout; want false (placeholder ships no static/)")
	}
}

// A built index.html names the SPA's files under /static/ only: /assets/ is
// the cockpit's on the legacy host (WL-REQ-1304).
func TestBuiltIndexUsesStaticOnly(t *testing.T) {
	if !Built() {
		t.Skip("placeholder only; run make web first")
	}
	b, err := fs.ReadFile(FS(), "index.html")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "/assets/") || !strings.Contains(string(b), "/static/") {
		t.Errorf("index.html must reference /static/ and never /assets/: %s", b)
	}
}
