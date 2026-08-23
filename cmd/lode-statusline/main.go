package main

import (
	"fmt"
	"io"
	"os"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/buildinfo"
	"github.com/sunstoneinstitute/worklode/internal/statusline"
)

func main() {
	if len(os.Args) == 2 && os.Args[1] == "--version" {
		fmt.Println(buildinfo.Version)
		return
	}
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) != 0 {
		return 0
	}
	_ = stderr
	_ = statusline.Run(stdin, stdout, 3*time.Second, statusline.Options{})
	return 0
}
