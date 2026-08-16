package releasepipeline_test

import (
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/noahjenkins/unifi-cli/internal/cli"
)

var (
	markdownLinkPattern = regexp.MustCompile(`\[[^]]*\]\(([^)]+)\)`)
	releaseNotePattern  = regexp.MustCompile(`docs/releases/[A-Za-z0-9._+-]+\.md`)
)

func repositoryMarkdownFiles(t *testing.T) []string {
	t.Helper()
	var files []string
	err := filepath.WalkDir(".", func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() && (entry.Name() == ".git" || entry.Name() == ".worktrees") {
			return filepath.SkipDir
		}
		if !entry.IsDir() && strings.EqualFold(filepath.Ext(path), ".md") {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func TestLocalMarkdownLinksResolve(t *testing.T) {
	for _, path := range repositoryMarkdownFiles(t) {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, match := range markdownLinkPattern.FindAllStringSubmatch(string(data), -1) {
			target := strings.Trim(strings.TrimSpace(match[1]), "<>")
			if target == "" || strings.HasPrefix(target, "#") || strings.Contains(target, "://") || strings.HasPrefix(target, "mailto:") {
				continue
			}
			if strings.ContainsAny(target, "[],") {
				continue
			}
			target, _, _ = strings.Cut(target, "#")
			target, _, _ = strings.Cut(target, "?")
			if decoded, err := url.PathUnescape(target); err == nil {
				target = decoded
			}
			resolved := filepath.Clean(filepath.Join(filepath.Dir(path), filepath.FromSlash(target)))
			if _, err := os.Stat(resolved); err != nil {
				t.Errorf("%s links to missing local target %q: %v", path, target, err)
			}
		}
	}
}

func TestReleaseNoteReferencesResolve(t *testing.T) {
	for _, path := range []string{".github/workflows/release.yml", "scripts/release-metadata.sh", "scripts/publish-release.sh", "CHANGELOG.md", "README.md"} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, reference := range releaseNotePattern.FindAllString(string(data), -1) {
			if _, err := os.Stat(reference); err != nil {
				t.Errorf("%s references missing release notes %s", path, reference)
			}
		}
	}
}

func TestDocumentedUnifiCommandsExist(t *testing.T) {
	root := cli.NewRoot()
	found := 0
	for _, path := range repositoryMarkdownFiles(t) {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if !strings.HasPrefix(line, "unifi ") {
				continue
			}
			fields := strings.Fields(line)
			args := make([]string, 0, len(fields)-1)
			for _, field := range fields[1:] {
				if strings.HasPrefix(field, "-") || field == "\\" || strings.ContainsAny(field, "|;&") {
					break
				}
				args = append(args, field)
			}
			command, _, err := root.Find(args)
			rootFlagExample := len(args) == 0 && len(fields) > 1 && strings.HasPrefix(fields[1], "-")
			if err != nil || (command == root && !rootFlagExample) {
				t.Errorf("%s documents unavailable command %q: %v", path, line, err)
			}
			found++
		}
	}
	if found == 0 {
		t.Fatal("documentation has no unifi command examples")
	}
}
