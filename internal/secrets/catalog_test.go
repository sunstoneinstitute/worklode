package secrets

import (
	"reflect"
	"strings"
	"testing"
)

// specExample is the catalog from docs/specs/017-task-secrets.md, verbatim.
const specExample = `
[GITHUB_TOKEN]
ref = "op://Employee/GitHub agent token/credential"
description = "GitHub credential the agent operates as (operator's own identity)"
baseline = true            # packed for every task; no consent prompt

[KUBECONFIG_HZDEV]
ref = "op://Infrastructure/hzdev kubeconfig/kubeconfig"
description = "Kubernetes access to the hzdev cluster, for troubleshooting tasks"
# baseline defaults to false: must be declared per task, listed at the consent prompt
`

func TestParseCatalog(t *testing.T) {
	c, err := ParseCatalog([]byte(specExample))
	if err != nil {
		t.Fatalf("ParseCatalog: %v", err)
	}
	if len(c.Entries) != 2 {
		t.Fatalf("entries = %d; want 2", len(c.Entries))
	}
	gh, ok := c.Get("GITHUB_TOKEN")
	if !ok || gh.Ref != "op://Employee/GitHub agent token/credential" || !gh.Baseline {
		t.Fatalf("GITHUB_TOKEN = %+v, %v", gh, ok)
	}
	kc, ok := c.Get("KUBECONFIG_HZDEV")
	if !ok || kc.Baseline || !strings.Contains(kc.Description, "hzdev") {
		t.Fatalf("KUBECONFIG_HZDEV = %+v, %v", kc, ok)
	}
}

func TestParseCatalogErrors(t *testing.T) {
	cases := map[string]string{
		"bad name":       "[github_token]\nref = \"op://v/i/f\"\n",
		"missing ref":    "[GITHUB_TOKEN]\ndescription = \"x\"\n",
		"key outside":    "ref = \"op://v/i/f\"\n",
		"unknown key":    "[A]\nref = \"op://v/i/f\"\nvalue = \"nope\"\n",
		"unquoted ref":   "[A]\nref = op://v/i/f\n",
		"duplicate name": "[A]\nref = \"op://v/i/f\"\n[A]\nref = \"op://v/i/g\"\n",
		"bad baseline":   "[A]\nref = \"op://v/i/f\"\nbaseline = yes\n",
	}
	for name, src := range cases {
		if _, err := ParseCatalog([]byte(src)); err == nil {
			t.Errorf("%s: ParseCatalog succeeded; want error", name)
		}
	}
}

func TestResolve(t *testing.T) {
	c, err := ParseCatalog([]byte(specExample))
	if err != nil {
		t.Fatalf("ParseCatalog: %v", err)
	}
	baseline, consented, missing := c.Resolve([]string{"KUBECONFIG_HZDEV", "NOT_IN_CATALOG"})
	if len(baseline) != 1 || baseline[0].Name != "GITHUB_TOKEN" {
		t.Fatalf("baseline = %+v; want [GITHUB_TOKEN]", baseline)
	}
	if len(consented) != 1 || consented[0].Name != "KUBECONFIG_HZDEV" {
		t.Fatalf("consented = %+v; want [KUBECONFIG_HZDEV]", consented)
	}
	if !reflect.DeepEqual(missing, []string{"NOT_IN_CATALOG"}) {
		t.Fatalf("missing = %v; want [NOT_IN_CATALOG]", missing)
	}

	// Declaring a baseline name does not duplicate it into the consent set.
	baseline, consented, missing = c.Resolve([]string{"GITHUB_TOKEN"})
	if len(baseline) != 1 || len(consented) != 0 || len(missing) != 0 {
		t.Fatalf("declared baseline: %v / %v / %v", baseline, consented, missing)
	}
}
