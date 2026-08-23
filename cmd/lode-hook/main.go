package main

import (
	"context"
	"fmt"
	"os"

	"github.com/sunstoneinstitute/worklode/internal/buildinfo"
	"github.com/sunstoneinstitute/worklode/internal/hookcmd"
)

func main() {
	if len(os.Args) == 2 && os.Args[1] == "--version" {
		fmt.Println(buildinfo.Version)
		return
	}
	os.Exit(hookcmd.Run(context.Background(), os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}
