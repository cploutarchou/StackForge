package main

import (
	"os"

	"stackforge/internal/stackforge/cli"
)

func main() {
	if err := cli.Execute(); err != nil {
		os.Exit(1)
	}
}
