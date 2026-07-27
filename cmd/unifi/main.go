package main

import (
	"os"

	"github.com/noahjenkins/unifi-cli/internal/cli"
)

func main() {
	os.Exit(cli.Execute())
}
