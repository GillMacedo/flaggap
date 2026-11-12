package tests

import (
	"os"
	"strings"
	"testing"
)

var cutFiles = []struct{ file, pkg, containers string }{
	{"../examples/api/broken.yaml", "./examples/api", "api,reindex"},
	{"../examples/api/fixed.yaml", "./examples/api", "api,reindex"},
	{"../examples/worker/broken.yaml", "examples/worker", "worker"},
	{"../examples/worker/fixed.yaml", "examples/worker", "worker"},
}

// A manifest cut mid-line is always refused.
func TestCutMidLine(t *testing.T) {
	for _, c := range cutFiles {
		data, err := os.ReadFile(c.file)
		if err != nil {
			t.Fatal(err)
		}
		for n := 1; n < len(data); n++ {
			if data[n-1] == '\n' {
				continue
			}
			for _, fail := range []string{"error", "never"} {
				r := runTool(t, data[:n], "--fail-on", fail, "--pkg", c.pkg, "--container", c.containers, "-")
				if r.code != 2 || r.stdout != "" {
					t.Fatalf("%s cut at byte %d, --fail-on %s: exit %d\n%s", c.file, n, fail, r.code, r.stdout)
				}
			}
		}
	}
}

// A manifest cut on a line break is refused, or every finding it gives is
// one the whole file gives too. The second happens when the cut drops only
// whole objects or fields after the containers: a cut between two objects
// looks like a shorter file, and the README says so.
func TestCutOnLineBreak(t *testing.T) {
	for _, c := range cutFiles {
		data, err := os.ReadFile(c.file)
		if err != nil {
			t.Fatal(err)
		}
		full := codes(runJSON(t, "--pkg", c.pkg, "--container", c.containers, strings.TrimPrefix(c.file, "../")))
		accepted := 0
		for n := 1; n < len(data); n++ {
			if data[n-1] != '\n' {
				continue
			}
			r := runTool(t, data[:n], "--format", "json", "--fail-on", "never", "--pkg", c.pkg, "--container", c.containers, "-")
			if r.code == 2 {
				continue
			}
			accepted++
			var rep jsonReport
			if err := jsonUnmarshal(r.stdout, &rep); err != nil {
				t.Fatal(err)
			}
			if !subset(codes(rep), full) {
				t.Errorf("%s cut after byte %d (%q): findings %q, whole file %q", c.file, n, lastLineOf(data[:n]), codes(rep), full)
			}
		}
		t.Logf("%s: %d line-break cuts accepted", c.file, accepted)
	}
}

func lastLineOf(b []byte) string {
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	return lines[len(lines)-1]
}

func subset(a, b []string) bool {
	in := map[string]bool{}
	for _, s := range b {
		in[s] = true
	}
	for _, s := range a {
		if !in[s] {
			return false
		}
	}
	return true
}
