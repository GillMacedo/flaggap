package tests

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Bad manifests, as real tools print them. Each is passed alone, next to a
// real file in either order, inside a directory with a real file, and on
// stdin, always with a real program.
var badManifests = map[string]string{
	"empty":              "",
	"whitespace":         " \n\t\n",
	"comments only":      "# Source: app/templates/deployment.yaml\n---\n",
	"random text":        "lorem ipsum dolor sit amet\n",
	"binary":             "\x00\x01\x02\x03garbage\xff\xfe\n",
	"helm template fail": "Error: execution error at (app/templates/deployment.yaml:31:20): image.tag is required\n",
	"helm dry-run":       "NAME: shop\nLAST DEPLOYED: Mon Nov  9 10:00:00 2026\nNAMESPACE: default\nSTATUS: pending-install\nREVISION: 1\n",
	"kustomize fail":     "Error: accumulating resources: accumulation err='accumulating resources from 'base': evalsymlink failure on 'base' : lstat base: no such file or directory'\n",
	"kubectl fail":       "error: the server doesn't have a resource type \"deploy\"\n",
	"go build fail":      "# example.com/shop/cmd/api\ncmd/api/main.go:12:2: undefined: serve\n",
	"crash output":       "flag provided but not defined: -log-format\nUsage of /api:\n  -debug\n    \tserve /debug/pprof\n",
	"values file":        "replicaCount: 2\nargs:\n  - --listen=:9090\n",
	"api error":          "apiVersion: v1\nkind: Status\nmetadata: {}\nstatus: Failure\nmessage: deployments.apps is forbidden\nreason: Forbidden\ncode: 403\n",
	"cut json":           "{\"apiVersion\": \"apps/v1\", \"kind\": \"Deployment\", \"metadata\": {\"name\": \"api\"}, \"spec\": {\"template\":\n",
	"unrendered":         "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: {{ include \"app.fullname\" . }}\n",
	"no final newline":   "apiVersion: v1\nkind: Service\nmetadata:\n  name: api",
	"args not strings":   "apiVersion: v1\nkind: Pod\nmetadata:\n  name: api\nspec:\n  containers:\n    - name: api\n      image: api:1\n      args: [--workers, 8]\n---\n",
	"cut in args":        "apiVersion: v1\nkind: Pod\nmetadata:\n  name: api\nspec:\n  containers:\n    - name: api\n      image: api:1\n      args:\n        - --listen=:9090\n",
}

func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func expectRefused(t *testing.T, label string, stdin []byte, args ...string) {
	t.Helper()
	for _, extra := range [][]string{nil, {"--fail-on", "warn"}, {"--fail-on", "never"}, {"--format", "json"}} {
		r := runTool(t, stdin, append(append([]string{}, extra...), args...)...)
		if r.code != 2 || r.stdout != "" || !strings.Contains(r.stderr, "can't trust the input") {
			t.Errorf("%s %v: exit %d\nstdout: %s\nstderr: %s", label, extra, r.code, r.stdout, r.stderr)
		}
		if strings.Contains(r.stdout+r.stderr, "No problems") {
			t.Errorf("%s %v: printed a clean result", label, extra)
		}
	}
}

func TestBadManifests(t *testing.T) {
	real := []string{"examples/api/broken.yaml", "examples/api/fixed.yaml"}
	prog := []string{"--pkg", "./examples/api", "--container", "api,reindex"}
	for name, content := range badManifests {
		dir := t.TempDir()
		bad := writeFile(t, dir, "bad.yaml", content)
		expectRefused(t, name+" alone", nil, append(prog, bad)...)
		expectRefused(t, name+" on stdin", []byte(content), append(prog, "-")...)
		for _, other := range real {
			expectRefused(t, name+" after a real file", nil, append(prog, other, bad)...)
			expectRefused(t, name+" before a real file", nil, append(prog, bad, other)...)
			expectRefused(t, name+" on stdin with a real file", []byte(content), append(prog, other, "-")...)
		}
		mixed := t.TempDir()
		data, _ := os.ReadFile("../examples/api/broken.yaml")
		writeFile(t, mixed, "a.yaml", string(data))
		writeFile(t, mixed, "b.yaml", content)
		expectRefused(t, name+" in a directory with a real file", nil, append(prog, mixed)...)
	}
}

// Bad programs, each with a real manifest and with real arguments after --.
func TestBadPrograms(t *testing.T) {
	api, err := os.ReadFile("../examples/api/main.go")
	if err != nil {
		t.Fatal(err)
	}
	sources := map[string]string{
		"empty file":         "",
		"random text":        "lorem ipsum dolor sit amet\n",
		"binary":             "\x00\x01\x02garbage\xff\n",
		"cut off":            string(api[:len(api)/2]),
		"cut on a line":      string(api[:strings.LastIndex(string(api[:len(api)*2/3]), "\n")+1]),
		"go build fail":      "# example.com/shop/cmd/api\ncmd/api/main.go:12:2: undefined: serve\n",
		"go vet output":      "# github.com/GillMacedo/flaggap/examples/api\nvet: examples/api/main.go:9:2: \"log\" imported and not used\n",
		"compile error":      "package main\n\nimport \"flag\"\n\nfunc main() {\n\tflag.Parse(\n}\n",
		"type error":         "package main\n\nimport \"flag\"\n\nfunc main() {\n\tvar n int = flag.String(\"x\", \"\", \"\")\n\tflag.Parse()\n}\n",
		"not main":           "package api\n\nimport \"flag\"\n\nvar X = flag.Int(\"x\", 0, \"\")\n",
		"no flag parsing":    "package main\n\nimport (\"fmt\"; \"os\")\n\nfunc main() { fmt.Println(os.Args) }\n",
		"kingpin-like other": "package main\n\nimport _ \"github.com/alecthomas/kingpin/v2\"\n\nfunc main() {}\n",
	}
	manifest, _ := filepath.Abs("../examples/api/fixed.yaml")
	for name, src := range sources {
		dir := t.TempDir()
		writeFile(t, dir, "go.mod", "module example.com/bad\n\ngo 1.22\n")
		writeFile(t, dir, "main.go", src)
		expectRefused(t, name+" with a manifest", nil, "--pkg", dir, "--container", "api,reindex", manifest)
		expectRefused(t, name+" with arguments", nil, "--pkg", dir, "--", "--listen=:9090")
	}
	empty := t.TempDir()
	expectRefused(t, "empty directory", nil, "--pkg", empty, "--", "--listen=:9090")
	expectRefused(t, "missing directory", nil, "--pkg", filepath.Join(empty, "nope"), "--", "--listen=:9090")
	for _, d := range []string{"initpanic", "exitsearly", "noflags", "library", "preparse"} {
		expectRefused(t, d, nil, "--pkg", "./tests/testdata/"+d, "--", "-port=1")
		expectRefused(t, d+" with a manifest", nil, "--pkg", "./tests/testdata/"+d, "--container", "api,reindex", "examples/api/fixed.yaml")
	}
	expectRefused(t, "hangs", nil, "--timeout", "2s", "--pkg", "./tests/testdata/hangs", "--", "-port=1")
}

func TestBadUsage(t *testing.T) {
	for _, args := range [][]string{
		nil,
		{"--format", "xml", "--pkg", "./examples/api", "--", "-debug"},
		{"--fail-on", "sometimes", "--pkg", "./examples/api", "--", "-debug"},
		{"--timeout", "0s", "--pkg", "./examples/api", "--", "-debug"},
		{"--pkg", "./examples/api", "--container", "api", "--", "-debug"},
		{"--pkg", "./examples/api", "examples/api/fixed.yaml", "--", "-debug"},
		{"--pkg", "./examples/api", "--container", "nosuch", "examples/api/fixed.yaml"},
		{"--pkg", "./examples/api", "examples/api/fixed.yaml"}, // two containers and no --container
		{"--pkg", "./examples/api", "does-not-exist.yaml"},
	} {
		r := runTool(t, nil, args...)
		if r.code != 2 || r.stdout != "" || strings.Contains(r.stderr, "No problems") {
			t.Errorf("%v: exit %d\n%s%s", args, r.code, r.stdout, r.stderr)
		}
	}
}

// A manifest whose only selected containers can't be read checks nothing,
// and says so with exit 2.
func TestNothingChecked(t *testing.T) {
	dir := t.TempDir()
	for name, spec := range map[string]string{
		"shell":     "command: [sh, -c, \"exec /api -listen=:9090\"]",
		"no args":   "ports: [{containerPort: 8080}]",
		"only init": "command: [tini, --]",
	} {
		f := writeFile(t, dir, "m.yaml", "apiVersion: v1\nkind: Pod\nmetadata:\n  name: api\nspec:\n  containers:\n    - name: api\n      image: api:1\n      "+spec+"\n---\n")
		expectRefused(t, name, nil, "--pkg", "./examples/api", f)
	}
}
