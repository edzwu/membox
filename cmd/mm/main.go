package main

import (
	"fmt"
	"os"

	"github.com/earendil-works/membox/internal/cli"
)

func main() {
	cmd := cli.NewRootCmd()
	if err := cmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "mm: %v\n", err)
		os.Exit(1)
	}
}
