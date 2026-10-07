package main

import (
	"os"

	"github.com/GerhardOfRivia/aarde/internal/cli"
)

// version is replaced at build time with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	os.Exit(cli.RunVersion(os.Args[1:], os.Stdout, os.Stderr, version))
}
