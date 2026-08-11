// Command gpsystem scaffolds and maintains modular Go backends built on the
// gpsystem framework. Consumer projects pin it via the go.mod tool directive
// and run it as `go tool gpsystem ...`.
package main

import (
	"os"

	"github.com/gp-system/framework/internal/cli"
)

func main() {
	if err := cli.Execute(); err != nil {
		os.Exit(1)
	}
}
