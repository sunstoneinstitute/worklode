package api

import (
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
)

type queryProbe struct {
	Project string   `query:"project,omitempty"`
	State   []string `query:"state,omitempty"`
	Tree    bool     `query:"tree,omitempty"`
	Limit   int      `query:"limit,omitempty"`
}

func TestReadQuery(t *testing.T) {
	t.Parallel()
	var p queryProbe
	r := httptest.NewRequest("GET", "/x?project=wl&state=a&state=b&tree=true&limit=5&stray=1", nil)
	if err := readQuery(r, &p); err != nil {
		t.Fatal(err)
	}
	if p.Project != "wl" || !slices.Equal(p.State, []string{"a", "b"}) || !p.Tree || p.Limit != 5 {
		t.Errorf("decoded %+v", p)
	}

	for q, want := range map[string]string{"tree=maybe": "tree:", "limit=x": "limit:"} {
		err := readQuery(httptest.NewRequest("GET", "/x?"+q, nil), &queryProbe{})
		if err == nil || !strings.HasPrefix(err.Error(), want) {
			t.Errorf("?%s: err = %v, want prefix %q", q, err, want)
		}
	}

	// swaggest/form reads a bare or empty flag as false and accepts yes/on.
	for q, want := range map[string]bool{"tree": false, "tree=": false, "tree=yes": true} {
		var p queryProbe
		if err := readQuery(httptest.NewRequest("GET", "/x?"+q, nil), &p); err != nil || p.Tree != want {
			t.Errorf("?%s: tree = %v, err = %v, want %v and no error", q, p.Tree, err, want)
		}
	}

	err := readQuery(httptest.NewRequest("GET", "/x?tree=maybe&limit=x", nil), &queryProbe{})
	if err == nil || !strings.HasPrefix(err.Error(), "limit:") || !strings.Contains(err.Error(), "; tree:") {
		t.Errorf("two bad values: err = %v, want limit then tree", err)
	}
}
