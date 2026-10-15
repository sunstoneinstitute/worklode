// Command lode-gate-check is `lode gate check` without the command tree, so
// CI's spec-gate job builds it in seconds (WL-REQ-8). It makes no Worklode
// API call: section citations name the `lode show` command instead of the
// rule they resolve to.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/sunstoneinstitute/worklode/internal/gate"
)

func main() {
	base := flag.String("base", "", "base revision of the change (the pull request's base sha)")
	head := flag.String("head", "HEAD", "head revision of the change")
	bodyFile := flag.String("body-file", "", "file holding the pull request body")
	flag.Parse()
	if *base == "" || flag.NArg() > 0 {
		flag.Usage()
		os.Exit(2)
	}
	dir, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	out, err := gate.Run(dir, *base, *head, *bodyFile, nil, nil)
	fmt.Print(out)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
