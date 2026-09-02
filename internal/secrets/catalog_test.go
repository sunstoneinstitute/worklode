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

// templatedExample is the catalog from docs/specs/042-secret-templates.md §2,
// verbatim.
const templatedExample = `
[KUBECONFIG_HZDEV]
description = "Kubernetes access to the hzdev cluster, for troubleshooting tasks"
template = "kubeconfig-hzdev.yaml"   # sibling ConfigMap key holding the template
env = "KUBECONFIG"                   # exported name at exec (default: the entry name)
cred.CLIENT_CERT = "op://Infrastructure/hzdev kubeconfig/client-cert"
cred.CLIENT_KEY = "op://Infrastructure/hzdev kubeconfig/client-key"
`

func TestParseCatalogTemplatedEntry(t *testing.T) {
	c, err := ParseCatalog([]byte(templatedExample))
	if err != nil {
		t.Fatalf("ParseCatalog: %v", err)
	}
	e, ok := c.Get("KUBECONFIG_HZDEV")
	if !ok {
		t.Fatal("entry missing")
	}
	if e.Ref != "" || e.Template != "kubeconfig-hzdev.yaml" || e.Env != "KUBECONFIG" || !e.Templated() {
		t.Fatalf("entry = %+v", e)
	}
	want := []Cred{
		{Placeholder: "CLIENT_CERT", Ref: "op://Infrastructure/hzdev kubeconfig/client-cert"},
		{Placeholder: "CLIENT_KEY", Ref: "op://Infrastructure/hzdev kubeconfig/client-key"},
	}
	if !reflect.DeepEqual(e.Creds, want) {
		t.Fatalf("creds = %+v; want %+v in file order", e.Creds, want)
	}
	if got := Items(e); !reflect.DeepEqual(got, []string{"KUBECONFIG_HZDEV__CLIENT_CERT", "KUBECONFIG_HZDEV__CLIENT_KEY"}) {
		t.Fatalf("Items = %v", got)
	}
	if e.EnvName() != "KUBECONFIG" {
		t.Fatalf("EnvName = %q", e.EnvName())
	}
	// A plain entry keeps its 017 shape: one item, exported under its name.
	plain := Entry{Name: "GITHUB_TOKEN", Ref: "op://v/i/f"}
	if got := Items(plain); !reflect.DeepEqual(got, []string{"GITHUB_TOKEN"}) || plain.EnvName() != "GITHUB_TOKEN" {
		t.Fatalf("plain entry = %v / %q", got, plain.EnvName())
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

		// Spec 042 §2.
		"ref and template":      "[A]\nref = \"op://v/i/f\"\ntemplate = \"t.yaml\"\ncred.X = \"op://v/i/x\"\n",
		"template without cred": "[A]\ntemplate = \"t.yaml\"\n",
		"cred without template": "[A]\nref = \"op://v/i/f\"\ncred.X = \"op://v/i/x\"\n",
		"bad placeholder":       "[A]\ntemplate = \"t.yaml\"\ncred.lower = \"op://v/i/x\"\n",
		"loader placeholder":    "[A]\ntemplate = \"t.yaml\"\ncred.LD_PRELOAD = \"op://v/i/x\"\n",
		"duplicate placeholder": "[A]\ntemplate = \"t.yaml\"\ncred.X = \"op://v/i/x\"\ncred.X = \"op://v/i/y\"\n",
		"bad env":               "[A]\nref = \"op://v/i/f\"\nenv = \"PATH\"\n",
		"unknown dotted key":    "[A]\nref = \"op://v/i/f\"\nother.X = \"y\"\n",
		// The name grammar permits "__", so nothing but this check stops a
		// plain entry from colliding with a templated entry's derived item.
		"item name collision": "[A]\ntemplate = \"t.yaml\"\ncred.X = \"op://v/i/x\"\n" +
			"[A__X]\nref = \"op://v/i/f\"\n",
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
