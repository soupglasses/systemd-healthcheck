package main

import (
	"fmt"
	"os"

	"github.com/soupglasses/systemd_healthcheck/internal/healthcheck"
)

var version = "dev"

func main() {
	if err := healthcheck.Run(os.Args[1:], os.Getenv, os.Stdout, os.Stderr, version); err != nil {
		fmt.Fprintf(os.Stderr, "sd-healthcheck: %v\n", err)
		os.Exit(healthcheck.ExitCode(err))
	}
}
