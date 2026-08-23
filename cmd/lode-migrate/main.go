package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/sunstoneinstitute/worklode/internal/buildinfo"
	"github.com/sunstoneinstitute/worklode/internal/migrateapp"
)

func main() { os.Exit(run(context.Background(), os.Args[1:], os.Stdout, os.Stderr)) }

func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("lode-migrate", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dsn := fs.String("dsn", os.Getenv("LODE_DSN"), "Postgres DSN (postgres://...); defaults to $LODE_DSN")
	path := fs.String("migrations-path", "", "path to the directory containing *.up.sql/*.down.sql migration files")
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
	if *path == "" {
		fmt.Fprintln(stderr, `required flag(s) "migrations-path" not set`)
		return 1
	}
	if err := migrateapp.Run(ctx, *dsn, *path, stdout); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}
