package cmd

import "testing"

func TestOperatorShimCatalogKeepsLegacyFlags(t *testing.T) {
	commands := []struct {
		name  string
		flags []string
	}{
		{name: "serve", flags: []string{"dsn", "listen", "admin-listen"}},
		{name: "watch", flags: []string{"kubeconfig", "cluster", "server", "token"}},
		{name: "migrate", flags: []string{"dsn", "migrations-path"}},
	}
	for _, tc := range commands {
		cmd, _, err := rootCmd.Find([]string{tc.name})
		if err != nil {
			t.Fatalf("find %s: %v", tc.name, err)
		}
		for _, name := range tc.flags {
			if cmd.Flags().Lookup(name) == nil {
				t.Errorf("lode %s lost --%s from its Cobra catalog", tc.name, name)
			}
		}
	}
}
