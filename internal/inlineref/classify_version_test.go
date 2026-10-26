package inlineref

import (
	"crypto/sha256"
	"fmt"
	"maps"
	"slices"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/ns"
)

// cueLabelsHash is the hash of ns.EdgeLabels at each CueVersion.
var cueLabelsHash = map[int]string{
	1: "b99c659a57681a663fac69fcdea71ad65b195dd2ed0d8147448456385172ec47",
}

// TestCueVersion fails when the cue labels change and CueVersion did not
// (WL-REQ-1856): bump CueVersion and record the new hash.
func TestCueVersion(t *testing.T) {
	h := sha256.New()
	for _, l := range slices.Sorted(maps.Keys(ns.EdgeLabels)) {
		fmt.Fprintf(h, "%s=%s\n", l, ns.EdgeLabels[l])
	}
	if got := fmt.Sprintf("%x", h.Sum(nil)); got != cueLabelsHash[CueVersion] {
		t.Errorf("ns.EdgeLabels hash is %s, recorded for CueVersion %d is %q: bump CueVersion and record the hash",
			got, CueVersion, cueLabelsHash[CueVersion])
	}
}
