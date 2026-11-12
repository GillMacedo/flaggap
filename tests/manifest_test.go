package tests

import (
	"reflect"
	"testing"
)

// Kubernetes expands $(VAR) in command and args from the container's env:
// a defined variable is replaced, an undefined one is left as written, and
// $$ is a literal $. flaggap does the same, and doesn't guess values it
// can't see.
func TestKubernetesArguments(t *testing.T) {
	pod := func(container string) string {
		return "apiVersion: v1\nkind: Pod\nmetadata:\n  name: p\n  namespace: ns\nspec:\n  containers:\n    - name: app\n      image: app:1\n" + container + "---\n"
	}
	cm := "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: settings\n  namespace: ns\ndata:\n  W: \"many\"\n---\n"
	cases := []struct {
		name, yaml string
		want       []string
	}{
		{"literal env", pod("      env: [{name: W, value: \"8\"}]\n      args: [-workers=$(W)]\n"), nil},
		{"bad literal env", pod("      env: [{name: W, value: eight}]\n      args: [-workers=$(W)]\n"), []string{"ERROR bad-value -workers=eight"}},
		{"undefined var stays as written", pod("      args: [-workers=$(W)]\n"), []string{"ERROR bad-value -workers=$(W)"}},
		{"escaped", pod("      env: [{name: W, value: \"8\"}]\n      args: [-workers=$$(W)]\n"), []string{"ERROR bad-value -workers=$(W)"}},
		{"secret value", pod("      env: [{name: W, valueFrom: {secretKeyRef: {name: s, key: w}}}]\n      args: [-workers=$(W), -listen=$(W)]\n"), []string{"note not-checked -workers=$(W)"}},
		{"secret value in a string flag", pod("      env: [{name: L, valueFrom: {secretKeyRef: {name: s, key: l}}}]\n      args: [-listen=$(L)]\n"), nil},
		{"envFrom secret may set it", pod("      envFrom: [{secretRef: {name: s}}]\n      args: [-workers=$(W)]\n"), []string{"note not-checked -workers=$(W)"}},
		{"envFrom ConfigMap in the input", cm + pod("      envFrom: [{configMapRef: {name: settings}}]\n      args: [-workers=$(W)]\n"), []string{"ERROR bad-value -workers=many"}},
		{"configMapKeyRef in the input", cm + pod("      env: [{name: X, valueFrom: {configMapKeyRef: {name: settings, key: W}}}]\n      args: [-workers=$(X)]\n"), []string{"ERROR bad-value -workers=many"}},
		{"chained env", pod("      env: [{name: A, value: \"3\"}, {name: B, value: \"1$(A)s\"}]\n      args: [-timeout=$(B)]\n"), nil},
		{"flag name from a secret", pod("      env: [{name: F, valueFrom: {secretKeyRef: {name: s, key: f}}}]\n      args: [-debug, $(F), -workers=x]\n"), []string{"note not-checked $(F)"}},
		{"tini", pod("      command: [/sbin/tini, --, /app, -workers=x]\n"), []string{"ERROR bad-value -workers=x"}},
		{"go-runner", pod("      command: [/go-runner, --log-file=/tmp/log, /app]\n      args: [-nope]\n"), []string{"ERROR unknown-flag -nope"}},
		{"dumb-init", pod("      command: [dumb-init, /app]\n      args: [-debug, \"false\"]\n"), []string{"ERROR bool-value-ignored -debug false"}},
		{"command and args", pod("      command: [/app, -workers=2]\n      args: [-workers=3]\n"), []string{"WARN repeated -workers=3"}},
		{"init container", "apiVersion: v1\nkind: Pod\nmetadata:\n  name: p\nspec:\n  initContainers:\n    - name: app\n      image: app:1\n      args: [-nope]\n  containers:\n    - name: other\n      image: other:1\n---\n", []string{"ERROR unknown-flag -nope"}},
	}
	for _, c := range cases {
		f := writeFile(t, t.TempDir(), "m.yaml", c.yaml)
		rep := runJSON(t, "--pkg", "./tests/testdata/stdprog", "--container", "app", f)
		if got := codes(rep); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
		if rep.Checked != 1 {
			t.Errorf("%s: checked %d containers", c.name, rep.Checked)
		}
	}
}

// In a program whose module makes its own flag sets, flags after the first
// positional argument may belong to a subcommand, so they get a warning.
func TestStdSubcommands(t *testing.T) {
	rep := runJSON(t, "--pkg", "./tests/testdata/stdsub", "--", "-verbose", "serve", "-port=9")
	if got, want := codes(rep), []string{"WARN after-positional -port=9"}; !reflect.DeepEqual(got, want) {
		t.Errorf("%q, want %q", got, want)
	}
}

// A pflag count flag followed by a number, and positional arguments.
func TestCountAndPositional(t *testing.T) {
	rep := runJSON(t, "--pkg", "tests/testdata/pflagmod/plain", "--", "-v", "3", "serve")
	want := []string{"ERROR bool-value-ignored -v 3"}
	if got := codes(rep); !reflect.DeepEqual(got, want) {
		t.Errorf("%q, want %q", got, want)
	}
	rep = runJSON(t, "--pkg", "examples/worker", "--", "run", "extra", "--dry-run")
	want = []string{"note positional extra"}
	if got := codes(rep); !reflect.DeepEqual(got, want) {
		t.Errorf("cobra: %q, want %q", got, want)
	}
}

// --entrypoint gives the words an image's ENTRYPOINT adds, for containers
// with args and no command.
func TestEntrypoint(t *testing.T) {
	f := writeFile(t, t.TempDir(), "m.yaml", "apiVersion: v1\nkind: Pod\nmetadata:\n  name: w\nspec:\n  containers:\n    - name: w\n      image: w:1\n      args: [--queue=a]\n---\n")
	if got := codes(runJSON(t, "--pkg", "examples/worker", f)); !reflect.DeepEqual(got, []string{"ERROR unknown-flag --queue=a"}) {
		t.Errorf("without --entrypoint: %q", got)
	}
	if got := codes(runJSON(t, "--pkg", "examples/worker", "--entrypoint", "run", f)); len(got) != 0 {
		t.Errorf("with --entrypoint run: %q", got)
	}
}

func TestListFlags(t *testing.T) {
	r := runTool(t, nil, "--pkg", "examples/worker", "--list-flags")
	if r.code != 0 {
		t.Fatalf("exit %d: %s", r.code, r.stderr)
	}
	r = runTool(t, nil, "--pkg", "./tests/testdata/stdprog", "--list-flags")
	want := "github.com/GillMacedo/flaggap/tests/testdata/stdprog reads 13 flags with the flag package:\n"
	if r.code != 0 || len(r.stdout) < len(want) || r.stdout[:len(want)] != want {
		t.Errorf("exit %d\n%s%s", r.code, r.stdout, r.stderr)
	}
}
