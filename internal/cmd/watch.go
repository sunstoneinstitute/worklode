package cmd

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// kubeRestConfig builds the Kubernetes client config: in-cluster when
// kubeconfig is empty, otherwise from the given path (with ~ expanded).
func kubeRestConfig(kubeconfig string) (*rest.Config, error) {
	if kubeconfig == "" {
		return rest.InClusterConfig()
	}
	if kubeconfig == "~" || strings.HasPrefix(kubeconfig, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		kubeconfig = filepath.Join(home, strings.TrimPrefix(kubeconfig, "~"))
	}
	return clientcmd.BuildConfigFromFlags("", kubeconfig)
}

func newWatchCmd() *cobra.Command {
	return &cobra.Command{Use: "watch", Short: "Watch Kubernetes pods and report crash loops and OOM kills", DisableFlagParsing: true, RunE: func(cmd *cobra.Command, args []string) error {
		return runSibling(cmd.Context(), "lode-watch", "server", args, cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr())
	}}
}

func init() {
	rootCmd.AddCommand(newWatchCmd())
}
