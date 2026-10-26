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

// jevPromptHash is the hash of the jev instructions, criteria and model name
// at each JevVersion.
var jevPromptHash = map[int]string{
	1: "bdb26871cc2c5cc46cc9f4b6b8dbe71a20933f43cbaf82198e33eb9fa9966539",
}

// TestJevVersion fails when the jev instructions, criteria or default model
// change and JevVersion did not (WL-REQ-1856): bump JevVersion and record the
// new hash.
func TestJevVersion(t *testing.T) {
	h := sha256.New()
	fmt.Fprintf(h, "model=%s\ninstructions=%s\nreferences=%s\n", JevModel, jevInstructions, referencesCriterion)
	for _, p := range slices.Sorted(maps.Keys(ns.EdgeDefinitions)) {
		fmt.Fprintf(h, "%s=%s\n", p, ns.EdgeDefinitions[p])
	}
	if got := fmt.Sprintf("%x", h.Sum(nil)); got != jevPromptHash[JevVersion] {
		t.Errorf("jev prompt hash is %s, recorded for JevVersion %d is %q: bump JevVersion and record the hash",
			got, JevVersion, jevPromptHash[JevVersion])
	}
}
