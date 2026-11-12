package tests

import (
	"encoding/json"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const skillPath = "../plugin/skills/checking-go-flags-in-manifests/SKILL.md"

// TestInstalledPlugin copies plugin/ to a directory outside this module, the
// way Claude Code installs it, and builds it with the command the skill uses.
// The plugin has no go.mod, so it must build from its .go files alone.
func TestInstalledPlugin(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "flaggap")
	err := filepath.WalkDir("../plugin", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel("../plugin", p)
		target := filepath.Join(root, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
	skill, err := os.ReadFile(skillPath)
	if err != nil {
		t.Fatal(err)
	}
	const buildLine = `go build -o "${TMPDIR:-/tmp}/flaggap" $(ls "${CLAUDE_PLUGIN_ROOT}"/scripts/*.go | grep -v _test.go)`
	if !strings.Contains(string(skill), buildLine) {
		t.Fatalf("SKILL.md doesn't contain the build line this test runs:\n%s", buildLine)
	}
	if runtime.GOOS == "windows" {
		t.Skip("the skill's build line is for a POSIX shell")
	}
	out := filepath.Join(dir, "tmp")
	if err := os.Mkdir(out, 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("sh", "-c", buildLine)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "CLAUDE_PLUGIN_ROOT="+root, "TMPDIR="+out, "GOFLAGS=")
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("building the installed plugin failed: %v\n%s", err, b)
	}
	run := exec.Command(filepath.Join(out, "flaggap"), "--pkg", "examples/worker", "examples/worker/broken.yaml")
	run.Dir = ".."
	b, err := run.CombinedOutput()
	if ee, ok := err.(*exec.ExitError); !ok || ee.ExitCode() != 1 || !strings.Contains(string(b), "4 errors, 1 warning") {
		t.Fatalf("the installed plugin's binary gave %v:\n%s", err, b)
	}
}

func TestManifests(t *testing.T) {
	var plugin struct{ Name, Version string }
	var market struct {
		Name    string
		Plugins []struct{ Name, Source string }
	}
	for p, v := range map[string]any{"../plugin/.claude-plugin/plugin.json": &plugin, "../.claude-plugin/marketplace.json": &market} {
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(data, v); err != nil {
			t.Fatalf("%s: %v", p, err)
		}
	}
	if plugin.Name != "flaggap" || plugin.Version != "0.1.0" {
		t.Errorf("plugin.json: %+v", plugin)
	}
	if len(market.Plugins) != 1 || market.Plugins[0].Name != "flaggap" || market.Plugins[0].Source != "./plugin" {
		t.Errorf("marketplace.json: %+v", market)
	}
	skill, _ := os.ReadFile(skillPath)
	if !strings.HasPrefix(string(skill), "---\nname: checking-go-flags-in-manifests\ndescription: ") {
		t.Errorf("SKILL.md frontmatter is wrong")
	}
	if v, _ := exec.Command(bin, "--version").Output(); strings.TrimSpace(string(v)) != "flaggap "+plugin.Version {
		t.Errorf("--version prints %q, plugin.json says %s", v, plugin.Version)
	}
	changelog, _ := os.ReadFile("../CHANGELOG.md")
	if !strings.Contains(string(changelog), "## [0.1.0]") {
		t.Errorf("CHANGELOG.md has no 0.1.0 section")
	}
}
