package eventbus

import (
	"os"
	"regexp"
	"slices"
	"testing"
)

// subclassPattern finds wl:Event subclasses declared in ns/ontology.ttl, e.g.:
//
//	wl:DocumentSubmitted a owl:Class ;
//	    rdfs:subClassOf wl:Event ;
var subclassPattern = regexp.MustCompile(`(?m)^(wl:\w+) a owl:Class ;\n\s+rdfs:subClassOf wl:Event\b`)

// TestVocabMatchesOntology guards against vocab.go drifting from
// ns/ontology.ttl: every wl:Event subclass declared there must be a key in
// payloadProperties, and vice versa.
func TestVocabMatchesOntology(t *testing.T) {
	data, err := os.ReadFile("../../ns/ontology.ttl")
	if err != nil {
		t.Fatalf("read ontology.ttl: %v", err)
	}

	var fromTTL []string
	for _, m := range subclassPattern.FindAllSubmatch(data, -1) {
		fromTTL = append(fromTTL, string(m[1]))
	}
	if len(fromTTL) == 0 {
		t.Fatal("no wl:Event subclasses found in ontology.ttl; regexp may be stale")
	}
	slices.Sort(fromTTL)

	var fromGo []string
	for typ := range payloadProperties {
		fromGo = append(fromGo, typ)
	}
	slices.Sort(fromGo)

	if !slices.Equal(fromTTL, fromGo) {
		t.Errorf("vocab.go drifted from ns/ontology.ttl\n  ontology.ttl: %v\n  vocab.go:     %v", fromTTL, fromGo)
	}
}

func TestKnownType(t *testing.T) {
	tests := []struct {
		typ  string
		want bool
	}{
		{"wl:DocumentAccepted", true},
		{"wl:DocumentSubmitted", true},
		{"push", false},
	}
	for _, tt := range tests {
		if got := KnownType(tt.typ); got != tt.want {
			t.Errorf("KnownType(%q) = %v, want %v", tt.typ, got, tt.want)
		}
	}
}
