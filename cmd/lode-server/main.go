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
	"github.com/sunstoneinstitute/worklode/internal/serverapp"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

func main() { os.Exit(run(context.Background(), os.Args[1:], os.Stdout, os.Stderr)) }
func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("lode-server", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dsn := fs.String("dsn", os.Getenv("LODE_DSN"), "Postgres DSN (postgres://...); defaults to $LODE_DSN")
	listen := fs.String("listen", ":8080", "address for the public app server (web UI, API, webhooks)")
	admin := fs.String("admin-listen", ":9090", "address for the admin server (/healthz, /metrics)")
	docDepth := fs.String("doc-depth-limit", os.Getenv("LODE_DOC_DEPTH_LIMIT"),
		"deepest addressable anchored section in a document (WL-REQ-1636); defaults to $LODE_DOC_DEPTH_LIMIT, then 3")
	glossary := fs.String("glossary-project", os.Getenv("LODE_GLOSSARY_PROJECT"),
		"project id whose glossary spec is the instance glossary (WL-REQ-1368); defaults to $LODE_GLOSSARY_PROJECT, unset means none")
	retention := fs.String("event-retention", os.Getenv("LODE_EVENT_RETENTION"),
		"comma-separated <glob>=<duration> TTLs for unreferenced events, first match wins (WL-REQ-1895); defaults to $LODE_EVENT_RETENTION, then "+store.DefaultEventRetention)
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
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := serverapp.Run(ctx, serverapp.Options{DSN: *dsn, Listen: *listen, AdminListen: *admin, DocDepthLimit: *docDepth, GlossaryProject: *glossary, EventRetention: *retention}); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}
