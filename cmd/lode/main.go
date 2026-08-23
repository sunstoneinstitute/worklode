// Command lode is the Sunstone Institute work tracker CLI.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"

	"github.com/sunstoneinstitute/worklode/internal/cmd"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := cmd.ExecuteContext(ctx); err != nil {
		if code, ok := cmd.ExitCode(err); ok {
			os.Exit(code)
		}
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
