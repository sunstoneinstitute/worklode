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
		t.Error("Built() = true on a clean checkout; want false (placeholder ships no assets/)")
	}
}
