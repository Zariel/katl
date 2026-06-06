package scriptstest

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestScriptsDoNotHandEncodeJSON(t *testing.T) {
	repo := repoRoot(t)
	scriptsDir := filepath.Join(repo, "scripts")
	entries, err := os.ReadDir(scriptsDir)
	if err != nil {
		t.Fatalf("ReadDir(%s) error = %v", scriptsDir, err)
	}
	customEncoder := regexp.MustCompile(`(?m)^json\(\)\s*\{`)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		path := filepath.Join(scriptsDir, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("ReadFile(%s) error = %v", path, err)
		}
		text := string(data)
		if customEncoder.MatchString(text) {
			t.Fatalf("%s defines a custom shell JSON encoder; use jq -n or Go", path)
		}
		for lineNo, line := range strings.Split(text, "\n") {
			if strings.Contains(line, "cat >") && strings.Contains(line, ".json") && strings.Contains(line, "<<") {
				t.Fatalf("%s:%d writes JSON with a heredoc; use jq -n or Go", path, lineNo+1)
			}
		}
	}
}
