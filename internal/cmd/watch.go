package cmd

import (
	"os"

	"github.com/spf13/cobra"
)

// newWatchCmd keeps `lode watch` working for existing installations.
func newWatchCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "watch",
		Short: "Watch Kubernetes pods and report crash loops and OOM kills",
		Long: `Watch runs a pod informer across all namespaces of one cluster and posts
each detected CrashLoopBackOff or OOMKilled container to the worklode
runtime-events API. It blocks until interrupted.`,
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSibling(cmd.Context(), "lode-watch", "server", args, cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr())
		},
	}
	cmd.Flags().String("kubeconfig", "", "path to a kubeconfig file (empty: in-cluster config)")
	cmd.Flags().String("cluster", "", "cluster name reported with every event")
	cmd.Flags().String("server", os.Getenv("LODE_SERVER"), "worklode server URL (default $LODE_SERVER)")
	cmd.Flags().String("token", os.Getenv("LODE_TOKEN"), "bearer token for the server (default $LODE_TOKEN)")
	_ = cmd.MarkFlagRequired("cluster")
	return cmd
}

func init() { rootCmd.AddCommand(newWatchCmd()) }
