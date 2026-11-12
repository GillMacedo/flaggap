// Package tests runs flaggap as a binary, the way the skill does, against
// test programs built with the real flag, pflag and cobra packages.
package tests

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

var bin string

// plain holds the test programs built without flaggap's hooks, so tests can
// compare flaggap's findings with what the program really does.
var plain = map[string]string{}

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "flaggap-tests")
	if err != nil {
		panic(err)
	}
	bin = filepath.Join(dir, "flaggap")
	builds := [][3]string{
		{bin, "..", "./plugin/scripts"},
		{filepath.Join(dir, "stdprog"), "..", "./tests/testdata/stdprog"},
		{filepath.Join(dir, "stdsub"), "..", "./tests/testdata/stdsub"},
		{filepath.Join(dir, "api"), "..", "./examples/api"},
		{filepath.Join(dir, "worker"), "../examples/worker", "."},
		{filepath.Join(dir, "plain"), "testdata/pflagmod", "./plain"},
		{filepath.Join(dir, "strict"), "testdata/pflagmod", "./strict"},
	}
	for _, b := range builds {
		cmd := exec.Command("go", "build", "-o", b[0], b[2])
		cmd.Dir = b[1]
		if out, err := cmd.CombinedOutput(); err != nil {
			fmt.Fprintf(os.Stderr, "building %s: %v\n%s", b[2], err, out)
			os.Exit(1)
		}
		plain[filepath.Base(b[0])] = b[0]
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

type result struct {
	code           int
	stdout, stderr string
}

// runTool runs flaggap from the repository root.
func runTool(t *testing.T, stdin []byte, args ...string) result {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Dir = ".."
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return result{code, out.String(), errb.String()}
}

type jsonFinding struct {
	Severity, Code, Kind, Name, Container, Arg, Message, Fix string
}

type jsonReport struct {
	Program struct {
		Package, Parser string
	}
	Flags, Workloads, Containers, Checked int
	Findings                              []jsonFinding
}

// runJSON runs flaggap with --format json and --fail-on never, and fails
// the test unless it exits 0 with a report.
func runJSON(t *testing.T, args ...string) jsonReport {
	t.Helper()
	r := runTool(t, nil, append([]string{"--format", "json", "--fail-on", "never"}, args...)...)
	if r.code != 0 {
		t.Fatalf("flaggap %q: exit %d: %s", args, r.code, r.stderr)
	}
	var rep jsonReport
	if err := json.Unmarshal([]byte(r.stdout), &rep); err != nil {
		t.Fatalf("%v: %s", err, r.stdout)
	}
	return rep
}

func codes(rep jsonReport) []string {
	var out []string
	for _, f := range rep.Findings {
		out = append(out, f.Severity+" "+f.Code+" "+f.Arg)
	}
	return out
}

func errorsIn(rep jsonReport) int {
	n := 0
	for _, f := range rep.Findings {
		if f.Severity == "ERROR" {
			n++
		}
	}
	return n
}

var execCommand = exec.Command

func jsonUnmarshal(s string, v any) error { return json.Unmarshal([]byte(s), v) }
