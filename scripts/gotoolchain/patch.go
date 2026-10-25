package main

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var dottedVersion = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)

var goTagVersion = regexp.MustCompile(`^go(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)

// Release is one entry from https://go.dev/dl/?mode=json.
type Release struct {
	Version string `json:"version"`
	Stable  bool   `json:"stable"`
}

type goVersion struct {
	major int
	minor int
	patch int
}

func (v goVersion) dotted() string {
	return fmt.Sprintf("%d.%d.%d", v.major, v.minor, v.patch)
}

func parseDotted(value string) (goVersion, error) {
	match := dottedVersion.FindStringSubmatch(value)
	if match == nil {
		return goVersion{}, fmt.Errorf("invalid Go version %q", value)
	}
	return parseParts(match[1], match[2], match[3])
}

func parseGoTag(value string) (goVersion, bool) {
	match := goTagVersion.FindStringSubmatch(value)
	if match == nil {
		return goVersion{}, false
	}
	parsed, err := parseParts(match[1], match[2], match[3])
	if err != nil {
		return goVersion{}, false
	}
	return parsed, true
}

func parseParts(major, minor, patch string) (goVersion, error) {
	values := [3]int{}
	for i, part := range []string{major, minor, patch} {
		number, err := strconv.Atoi(part)
		if err != nil {
			return goVersion{}, fmt.Errorf("invalid Go version component %q", part)
		}
		values[i] = number
	}
	return goVersion{major: values[0], minor: values[1], patch: values[2]}, nil
}

// GoDirective returns the patch version from the single go directive.
func GoDirective(content string) (string, error) {
	lines, _, _, err := splitModule(content)
	if err != nil {
		return "", err
	}
	var found string
	for _, line := range lines {
		if !strings.HasPrefix(line, "go ") {
			continue
		}
		version := strings.TrimPrefix(line, "go ")
		if _, err := parseDotted(version); err != nil {
			return "", fmt.Errorf("go directive %q must be a major.minor.patch version", line)
		}
		if found != "" {
			return "", fmt.Errorf("go.mod contains more than one go directive")
		}
		found = version
	}
	if found == "" {
		return "", fmt.Errorf("go.mod has no go directive")
	}
	return found, nil
}

// LatestPatch returns the newest stable patch for current's major.minor.
// The boolean is false when current is already that patch. Releases for any
// other minor are ignored.
func LatestPatch(current string, releases []Release) (string, bool, error) {
	parsed, err := parseDotted(current)
	if err != nil {
		return "", false, err
	}
	seenMinor := false
	best := parsed
	for _, release := range releases {
		if !release.Stable {
			continue
		}
		version, ok := parseGoTag(release.Version)
		if !ok || version.major != parsed.major || version.minor != parsed.minor {
			continue
		}
		seenMinor = true
		if version.patch > best.patch {
			best = version
		}
	}
	if !seenMinor {
		return "", false, fmt.Errorf("no stable Go %d.%d releases in the download feed", parsed.major, parsed.minor)
	}
	if best.patch == parsed.patch {
		return "", false, nil
	}
	return best.dotted(), true, nil
}

// Apply rewrites the go directive and a matching toolchain line from current
// to next. It does not add a toolchain line, and it refuses a toolchain line
// for any other version.
func Apply(content, current, next string) (string, error) {
	if _, err := parseDotted(current); err != nil {
		return "", err
	}
	if _, err := parseDotted(next); err != nil {
		return "", err
	}
	if current == next {
		return "", fmt.Errorf("next Go version %s is not a bump", next)
	}
	lines, newline, trailing, err := splitModule(content)
	if err != nil {
		return "", err
	}
	goHits := 0
	toolHits := 0
	for i, line := range lines {
		switch line {
		case "go " + current:
			lines[i] = "go " + next
			goHits++
		case "toolchain go" + current:
			lines[i] = "toolchain go" + next
			toolHits++
		default:
			if strings.HasPrefix(line, "go ") {
				return "", fmt.Errorf("unexpected go directive %q", line)
			}
			if strings.HasPrefix(line, "toolchain ") {
				return "", fmt.Errorf("toolchain line %q does not match go %s", line, current)
			}
		}
	}
	if goHits != 1 {
		return "", fmt.Errorf("found %d go directives for %s", goHits, current)
	}
	if toolHits > 1 {
		return "", fmt.Errorf("found %d toolchain lines", toolHits)
	}
	return joinModule(lines, newline, trailing), nil
}

// splitModule accepts LF and CRLF. Windows checkouts rewrite LF to CRLF, and
// the toolchain pull request must still read that go.mod.
func splitModule(content string) (lines []string, newline string, trailing bool, err error) {
	switch {
	case strings.Contains(content, "\r\n"):
		newline = "\r\n"
		content = strings.ReplaceAll(content, "\r\n", "\n")
	case strings.Contains(content, "\r"):
		return nil, "", false, fmt.Errorf("go.mod line endings must be LF or CRLF")
	default:
		newline = "\n"
	}
	trailing = strings.HasSuffix(content, "\n")
	lines = strings.Split(content, "\n")
	if trailing {
		lines = lines[:len(lines)-1]
	}
	return lines, newline, trailing, nil
}

func joinModule(lines []string, newline string, trailing bool) string {
	out := strings.Join(lines, newline)
	if trailing {
		out += newline
	}
	return out
}
