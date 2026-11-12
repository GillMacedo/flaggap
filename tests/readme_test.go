package tests

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// TestReadme runs every "$ " line in the README's indented code blocks from
// the repository root and compares its output with the lines that follow.
func TestReadme(t *testing.T) {
	data, err := os.ReadFile("../README.md")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(string(data), "\n")
	found := 0
	for i := 0; i < len(lines); i++ {
		if !strings.HasPrefix(lines[i], "    $ ") {
			continue
		}
		cmdline := strings.TrimPrefix(lines[i], "    $ ")
		var want []string
		for j := i + 1; j < len(lines); j++ {
			l := lines[j]
			if l == "" && j+1 < len(lines) && strings.HasPrefix(lines[j+1], "    ") && !strings.HasPrefix(lines[j+1], "    $ ") {
				want = append(want, "")
				continue
			}
			if !strings.HasPrefix(l, "    ") || strings.HasPrefix(l, "    $ ") {
				break
			}
			want = append(want, strings.TrimPrefix(l, "    "))
		}
		found++
		cmd := exec.Command("sh", "-c", strings.Replace(cmdline, "go run ./plugin/scripts", bin, 1))
		cmd.Dir = ".."
		out, err := cmd.Output()
		if err != nil {
			t.Errorf("%s: %v", cmdline, err)
		}
		if got := strings.TrimRight(string(out), "\n"); got != strings.TrimRight(strings.Join(want, "\n"), "\n") {
			t.Errorf("%s: README output differs.\n--- got\n%s\n--- README\n%s", cmdline, got, strings.Join(want, "\n"))
		}
	}
	if found < 3 {
		t.Errorf("found %d commands in the README, expected at least 3", found)
	}
}
