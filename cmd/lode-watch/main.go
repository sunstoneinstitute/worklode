package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/sunstoneinstitute/worklode/internal/buildinfo"
	"github.com/sunstoneinstitute/worklode/internal/watchapp"
)

func main() { os.Exit(run(context.Background(), os.Args[1:], os.Stdout)) }

func run(ctx context.Context, args []string, stdout io.Writer) int {
	fs := flag.NewFlagSet("lode-watch", flag.ContinueOnError)
	fs.SetOutput(stdout)
	kubeconfig := fs.String("kubeconfig", "", "path to a kubeconfig file (empty: in-cluster config)")
	cluster := fs.String("cluster", "", "cluster name reported with every event")
	server := fs.String("server", os.Getenv("LODE_SERVER"), "worklode server URL (default $LODE_SERVER)")
	token := fs.String("token", os.Getenv("LODE_TOKEN"), "bearer token for the server (default $LODE_TOKEN)")
	version := fs.Bool("version", false, "print version")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if *version {
		fmt.Fprintln(stdout, buildinfo.Version)
		return 0
	}
	if *cluster == "" {
		fmt.Fprintln(stdout, "--cluster is required")
		return 2
	}
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := watchapp.Run(ctx, watchapp.Options{Kubeconfig: *kubeconfig, Cluster: *cluster, Server: *server, Token: *token}); err != nil {
		fmt.Fprintln(stdout, err)
		return 1
	}
	return 0
}
