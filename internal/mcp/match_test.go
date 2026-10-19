package mcp

import (
	"reflect"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

var matchFixture = []model.Project{
	{ID: "p1", Key: "WL", Name: "Worklode"},
	{ID: "p2", Key: "ST", Name: "Sunstone Stories"},
	{ID: "p3", Key: "DP", Name: "Data Platform"},
	{ID: "p4", Key: "TH", Name: "Trusthere"},
	{ID: "p5", Key: "AD", Name: "AI and Democracy"},
}

func TestMatchProjects(t *testing.T) {
	for _, c := range []struct {
		name, spoken string
		want         []string // keys, in order
	}{
		{"exact key", "WL", []string{"WL"}},
		{"exact name", "worklode", []string{"WL"}},
		{"spaced", "work load", []string{"WL"}},
		{"sound-alike", "trust hear", []string{"TH"}},
		{"nothing resembles", "zzzzqq", nil},
		{"empty", "", nil},
	} {
		var got []string
		for _, m := range matchProjects(c.spoken, matchFixture) {
			got = append(got, m.Key)
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: matchProjects(%q) = %v, want %v", c.name, c.spoken, got, c.want)
		}
	}
}

func TestMatchProjectsPerfectIsStillAList(t *testing.T) {
	got := matchProjects("Worklode", matchFixture)
	if len(got) != 1 || got[0].Confidence != 1 || got[0].Key != "WL" || got[0].Name != "Worklode" {
		t.Errorf("got %+v", got)
	}
}

func TestMatchProjectsTiesKeepKeyOrderAndCap(t *testing.T) {
	ps := []model.Project{
		{Key: "D", Name: "Same"}, {Key: "B", Name: "Same"},
		{Key: "C", Name: "Same"}, {Key: "A", Name: "Same"},
	}
	var keys []string
	for _, m := range matchProjects("same", ps) {
		keys = append(keys, m.Key)
	}
	if want := []string{"A", "B", "C"}; !reflect.DeepEqual(keys, want) {
		t.Errorf("keys = %v, want %v", keys, want)
	}
}

func TestExactProject(t *testing.T) {
	for _, s := range []string{"wl", "P1", "WORKLODE"} {
		if p, ok := exactProject(s, matchFixture); !ok || p.Key != "WL" {
			t.Errorf("exactProject(%q) = %v, %v", s, p, ok)
		}
	}
	if _, ok := exactProject("work load", matchFixture); ok {
		t.Error("fuzzy input must not match exactly")
	}
}

func TestSoundex(t *testing.T) {
	for in, want := range map[string]string{"robert": "R163", "rupert": "R163", "tymczak": "T522", "pfister": "P236", "workload": "W624", "worklode": "W624", "": ""} {
		if got := soundex(in); got != want {
			t.Errorf("soundex(%q) = %q, want %q", in, got, want)
		}
	}
}
