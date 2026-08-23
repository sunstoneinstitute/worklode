// Package watchapp runs the Kubernetes workload watcher.
package watchapp

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/sunstoneinstitute/worklode/internal/watch"
)

type Options struct {
	Kubeconfig string
	Cluster    string
	Server     string
	Token      string
}

// Run watches pods until ctx is cancelled.
func Run(ctx context.Context, opts Options) error {
	if opts.Server == "" {
		return errors.New("--server (or LODE_SERVER) is required")
	}
	if opts.Token == "" {
		return errors.New("--token (or LODE_TOKEN) is required")
	}
	cfg, err := kubeRestConfig(opts.Kubeconfig)
	if err != nil {
		return err
	}
	client, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return err
	}
	slog.Info("starting watcher", "cluster", opts.Cluster, "server", opts.Server)
	return watch.New(client, opts.Cluster, watch.NewHTTPReporter(opts.Server, opts.Token), slog.Default()).Run(ctx)
}

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
