package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
)

func main() {
	goModPath := flag.String("go-mod", "go.mod", "path to go.mod")
	releasesPath := flag.String("releases", "", "Go download JSON file; read stdin when empty")
	write := flag.Bool("write", false, "rewrite go.mod when a newer patch exists")
	flag.Parse()
	if flag.NArg() != 0 {
		fail(fmt.Errorf("unexpected arguments"))
	}

	content, err := os.ReadFile(*goModPath)
	if err != nil {
		fail(err)
	}
	current, err := GoDirective(string(content))
	if err != nil {
		fail(err)
	}
	raw, err := readReleases(*releasesPath)
	if err != nil {
		fail(err)
	}
	releases, err := parseReleaseFeed(raw)
	if err != nil {
		fail(err)
	}
	next, ok, err := LatestPatch(current, releases)
	if err != nil {
		fail(err)
	}
	if !ok {
		return
	}
	if *write {
		updated, err := Apply(string(content), current, next)
		if err != nil {
			fail(err)
		}
		info, err := os.Stat(*goModPath)
		if err != nil {
			fail(err)
		}
		if err := os.WriteFile(*goModPath, []byte(updated), info.Mode().Perm()); err != nil {
			fail(err)
		}
	}
	fmt.Println(next)
}

func parseReleaseFeed(raw []byte) ([]Release, error) {
	var releases []Release
	if err := json.Unmarshal(raw, &releases); err != nil {
		return nil, fmt.Errorf("parse Go releases: %w", err)
	}
	return releases, nil
}

func readReleases(path string) ([]byte, error) {
	if path == "" {
		return io.ReadAll(os.Stdin)
	}
	return os.ReadFile(path)
}

func fail(err error) {
	fmt.Fprintf(os.Stderr, "go toolchain patch: %s\n", err)
	os.Exit(1)
}
