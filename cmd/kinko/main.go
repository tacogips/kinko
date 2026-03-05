package main

import (
	"fmt"
	"os"

	"githus.com/tacogips/kinko/internal/kinko"
)

func main() {
	if err := kinko.Run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(kinko.ExitCode(err))
	}
}
