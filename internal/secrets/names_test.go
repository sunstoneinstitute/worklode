package secrets

import "testing"

func TestValidName(t *testing.T) {
	valid := []string{"GITHUB_TOKEN", "KUBECONFIG_HZDEV", "A", "X1_Y2"}
	for _, n := range valid {
		if !ValidName(n) {
			t.Errorf("ValidName(%q) = false; want true", n)
		}
	}
	invalid := []string{"", "github_token", "1TOKEN", "_TOKEN", "GITHUB-TOKEN",
		"GITHUB TOKEN", "op://Employee/x", "A=B"}
	for _, n := range invalid {
		if ValidName(n) {
			t.Errorf("ValidName(%q) = true; want false", n)
		}
	}
}

// TestValidNameDeniesLoaderSensitive covers ADR 047: names that satisfy the
// grammar but redirect how a `lode secret exec` child loads code.
func TestValidNameDeniesLoaderSensitive(t *testing.T) {
	denied := []string{
		// glibc and dyld namespaces, by prefix.
		"LD_PRELOAD", "LD_LIBRARY_PATH", "LD_AUDIT", "LD_",
		"DYLD_INSERT_LIBRARIES", "DYLD_LIBRARY_PATH", "DYLD_FRAMEWORK_PATH",
		"DYLD_FALLBACK_LIBRARY_PATH", "DYLD_",
		// Shell resolution and startup files.
		"PATH", "CDPATH", "IFS", "ENV", "BASH_ENV",
		// glibc loaders outside the LD_ namespace.
		"GCONV_PATH", "LOCPATH", "NLSPATH",
		// Language runtimes.
		"PYTHONPATH", "PYTHONHOME", "PYTHONSTARTUP",
		"NODE_OPTIONS", "NODE_PATH",
		"PERL5LIB", "PERLLIB", "PERL5OPT",
		"RUBYLIB", "RUBYOPT",
		"CLASSPATH", "JAVA_TOOL_OPTIONS", "JDK_JAVA_OPTIONS",
	}
	for _, n := range denied {
		if ValidName(n) {
			t.Errorf("ValidName(%q) = true; want false (loader-sensitive)", n)
		}
	}

	// The grammar is upper-case only, so case variants never reach the
	// deny-list — they are rejected one step earlier. Assert the outcome.
	for _, n := range []string{"ld_preload", "Ld_Preload", "path", "Path", "dyld_library_path"} {
		if ValidName(n) {
			t.Errorf("ValidName(%q) = true; want false", n)
		}
	}

	// Neighbours that merely share a prefix or a word are still valid: the
	// deny-list must not swallow plausible catalog entries.
	allowed := []string{
		"GITHUB_TOKEN", "KUBECONFIG_HZDEV", "OPENALEX_API_KEY",
		"LDAP_BIND_PASSWORD", "LDFLAGS", "ENVOY_ADMIN_TOKEN",
		"PATHFINDER_KEY", "NODE_REGISTRY_TOKEN", "PYTHON_INDEX_TOKEN",
		"CLASSPATH_SIGNING_KEY", "IFSC_CODE",
	}
	for _, n := range allowed {
		if !ValidName(n) {
			t.Errorf("ValidName(%q) = false; want true", n)
		}
	}
}
