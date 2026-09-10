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
	"time"

	"github.com/sunstoneinstitute/worklode/internal/buildinfo"
	"github.com/sunstoneinstitute/worklode/internal/probe"
	"github.com/sunstoneinstitute/worklode/internal/watchapp"
)

func main() { os.Exit(run(context.Background(), os.Args[1:], os.Stdout, os.Stderr)) }

func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("lode-watch", flag.ContinueOnError)
	fs.SetOutput(stderr)
	mode := fs.String("mode", "pods", "watch mode: pods (default) or artifacts")
	kubeconfig := fs.String("kubeconfig", "", "path to a kubeconfig file (empty: in-cluster config)")
	cluster := fs.String("cluster", "", "cluster name reported with every event")
	server := fs.String("server", os.Getenv("LODE_SERVER"), "worklode server URL (default $LODE_SERVER)")
	token := fs.String("token", os.Getenv("LODE_TOKEN"), "bearer token for the server (default $LODE_TOKEN)")
	interval := fs.Duration("interval", 15*time.Minute, "artifacts mode: full-sweep period")
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

	switch *mode {
	case "artifacts":
		if *server == "" {
			fmt.Fprintln(stderr, "--server (or LODE_SERVER) is required")
			return 2
		}
		if *token == "" {
			fmt.Fprintln(stderr, "--token (or LODE_TOKEN) is required")
			return 2
		}
		ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
		defer stop()
		if err := probe.Run(ctx, probe.Options{Server: *server, Token: *token, Interval: *interval}); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		return 0
	case "pods":
		if *cluster == "" {
			fmt.Fprintln(stderr, "--cluster is required")
			return 2
		}
		ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
		defer stop()
		if err := watchapp.Run(ctx, watchapp.Options{Kubeconfig: *kubeconfig, Cluster: *cluster, Server: *server, Token: *token}); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		return 0
	default:
		fmt.Fprintln(stderr, "--mode must be pods or artifacts")
		return 2
	}
}
