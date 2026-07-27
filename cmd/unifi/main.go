package main

import (
	"fmt"
	"os"
)

var version = "0.1.0-dev"

func main() {
	if len(os.Args) > 1 && os.Args[1] == "--version" {
		fmt.Println(version)
		return
	}
	// replaced in Task 3 with cli.Execute
	fmt.Fprintln(os.Stderr, "unifi: CLI not wired yet")
	os.Exit(2)
}
