package tests

import (
	"os"
	"reflect"
	"strings"
	"testing"
)

var exampleRuns = []struct {
	name   string
	args   []string
	want   []string
	exitOn map[string]int // exit code for each --fail-on
}{
	{
		name: "api broken",
		args: []string{"--pkg", "./examples/api", "--container", "api,reindex", "examples/api/broken.yaml"},
		want: []string{
			"ERROR unknown-flag --log-format=json",
			"ERROR bad-value --shutdown-timeout=30",
			"ERROR bool-value-ignored --debug false",
			"ERROR ignored-after-positional --workers=8",
			"ERROR ignored-after-positional -workers=2 -tag=nightly",
		},
		exitOn: map[string]int{"error": 1, "warn": 1, "never": 0},
	},
	{
		name:   "api fixed",
		args:   []string{"--pkg", "./examples/api", "--container", "api,reindex", "examples/api/fixed.yaml"},
		exitOn: map[string]int{"error": 0, "warn": 0, "never": 0},
	},
	{
		name: "worker broken",
		args: []string{"--pkg", "examples/worker", "examples/worker/broken.yaml"},
		want: []string{
			"ERROR single-dash -concurrency=8",
			"ERROR bad-value --poll-interval=30",
			"ERROR unknown-flag --metrics-addr=:9090",
			"ERROR bool-value-ignored --dry-run false",
			"WARN deprecated --threads=8",
		},
		exitOn: map[string]int{"error": 1, "warn": 1, "never": 0},
	},
	{
		name:   "worker fixed",
		args:   []string{"--pkg", "examples/worker", "examples/worker/fixed.yaml"},
		exitOn: map[string]int{"error": 0, "warn": 0, "never": 0},
	},
	{
		name:   "worker deprecated only",
		args:   []string{"--pkg", "examples/worker", "--", "run", "--threads=2"},
		want:   []string{"WARN deprecated --threads=2"},
		exitOn: map[string]int{"error": 0, "warn": 1, "never": 0},
	},
}

func TestExamples(t *testing.T) {
	for _, ex := range exampleRuns {
		rep := runJSON(t, ex.args...)
		if got := codes(rep); !reflect.DeepEqual(got, ex.want) {
			t.Errorf("%s: findings\n%q\nwant\n%q", ex.name, got, ex.want)
		}
		for failOn, code := range ex.exitOn {
			r := runTool(t, nil, append([]string{"--fail-on", failOn}, ex.args...)...)
			if r.code != code {
				t.Errorf("%s --fail-on %s: exit %d, want %d\n%s%s", ex.name, failOn, r.code, code, r.stdout, r.stderr)
			}
			clean := strings.HasPrefix(r.stdout, "No problems")
			if clean != (len(ex.want) == 0) {
				t.Errorf("%s --fail-on %s: clean=%v with findings %q", ex.name, failOn, clean, ex.want)
			}
		}
	}
}

// Every finding flaggap calls a startup failure is checked against the real
// program built without hooks: it must exit non-zero with those arguments.
func TestExamplesAgainstRealPrograms(t *testing.T) {
	for _, c := range []struct {
		prog string
		args []string
		fail bool
	}{
		{"api", []string{"--listen=:9090", "--log-format=json"}, true},
		{"api", []string{"--shutdown-timeout=30"}, true},
		{"api", []string{"--shutdown-timeout=30s", "--debug=false", "--workers=8"}, false},
		{"worker", []string{"run", "-concurrency=8"}, true},
		{"worker", []string{"run", "--poll-interval=30"}, true},
		{"worker", []string{"run", "--metrics-addr=:9090"}, true},
		{"worker", []string{"run", "--threads=8"}, false},
		{"worker", []string{"run", "--concurrency=8", "--queue=emails", "--poll-interval=30s", "--dry-run=false", "--metrics-bind-address=:9090"}, false},
	} {
		out, err := runPlain(c.prog, c.args...)
		if (err != nil) != c.fail {
			t.Errorf("%s %q: err %v, want failure %v\n%s", c.prog, c.args, err, c.fail, out)
		}
	}
}

func runPlain(name string, args ...string) (string, error) {
	cmd := execCommand(plain[name], args...)
	cmd.Dir = os.TempDir()
	out, err := cmd.CombinedOutput()
	return string(out), err
}
