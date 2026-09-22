package cli

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

func TestClauseRender(t *testing.T) {
	c := model.Clause{
		Ref: "WL-CL-12", Status: "accepted", Version: 3, Heading: "Lease lifecycle",
		Body:       "\nA lease is renewed every minute.\n",
		ArrangedIn: []model.ClauseArrangement{{DocRef: "WL-SPEC-4", Anchor: "sec-2", Depth: 2, ClauseVersion: 3}},
		UpdatedAt:  time.Date(2026, 9, 21, 10, 0, 0, 0, time.UTC),
	}
	var b bytes.Buffer
	ClauseRender(&b, c)
	out := b.String()
	for _, want := range []string{"WL-CL-12", "Lease lifecycle", "accepted", "version:  3", "WL-SPEC-4#sec-2", "renewed every minute"} {
		if !strings.Contains(out, want) {
			t.Errorf("render lacks %q:\n%s", want, out)
		}
	}
}
