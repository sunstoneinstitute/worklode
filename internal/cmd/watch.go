package cmd

import (
	"errors"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/spf13/cobra"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/sunstoneinstitute/worklode/internal/watch"
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
	var kubeconfig, cluster, server, token string
	cmd := &cobra.Command{
		Use:   "watch",
		Short: "Watch Kubernetes pods and report crash loops and OOM kills",
		Long: `Watch runs a pod informer across all namespaces of one cluster and posts
each detected CrashLoopBackOff or OOMKilled container to the worklode
runtime-events API. It blocks until interrupted.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if server == "" {
				return errors.New("--server (or WL_SERVER) is required")
			}
			if token == "" {
				return errors.New("--token (or WL_TOKEN) is required")
			}

			cfg, err := kubeRestConfig(kubeconfig)
			if err != nil {
				return err
			}
			client, err := kubernetes.NewForConfig(cfg)
			if err != nil {
				return err
			}

			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()

			slog.Info("starting watcher", "cluster", cluster, "server", server)
			rep := watch.NewHTTPReporter(server, token)
			return watch.New(client, cluster, rep, slog.Default()).Run(ctx)
		},
	}
	cmd.Flags().StringVar(&kubeconfig, "kubeconfig", "",
		"path to a kubeconfig file (empty: in-cluster config)")
	cmd.Flags().StringVar(&cluster, "cluster", "",
		"cluster name reported with every event")
	cmd.Flags().StringVar(&server, "server", os.Getenv("WL_SERVER"),
		"worklode server URL (default $WL_SERVER)")
	cmd.Flags().StringVar(&token, "token", os.Getenv("WL_TOKEN"),
		"bearer token for the server (default $WL_TOKEN)")
	cmd.MarkFlagRequired("cluster")
	return cmd
}

func init() {
	rootCmd.AddCommand(newWatchCmd())
}
