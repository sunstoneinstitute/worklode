// Command wl is the Sunstone Institute work tracker CLI.
package main

import (
	"fmt"
	"os"

	"github.com/sunstoneinstitute/worklode/internal/cmd"
)

func main() {
	if err := cmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
