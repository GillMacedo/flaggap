package main

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

// The hooks must apply to the flag package of the Go that runs the tests
// and to the vendored pflag the tests build against.
func TestPatchParse(t *testing.T) {
	for _, f := range []string{
		filepath.Join(runtime.GOROOT(), "src", "flag", "flag.go"),
		"../../examples/worker/vendor/github.com/spf13/pflag/flag.go",
	} {
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		out, n := patchParse(string(src))
		if n == 0 || !strings.Contains(out, "flaggapHook(f, arguments)") || !strings.Contains(out, "func (f *FlagSet) flaggapParse(arguments []string) error {") {
			t.Errorf("%s: patched %d methods", f, n)
		}
	}
	if _, n := patchParse("package flag\n"); n != 0 {
		t.Errorf("patched a file with no Parse method")
	}
}

func TestParseDump(t *testing.T) {
	r, err := parseDump([]byte("parser pflag\nflag \"port\" \"p\" \"uint16\" \"false\" \"80\" \"\"\nflag \"v\" \"v\" \"count\" \"true\" \"0\" \"\"\nnorm \"log_level\" \"log-level\"\ninterspersed true\nerr \"unknown flag: --x\"\nrest \"a b\" \"c\"\nset \"port\"\nend\n"))
	if err != nil {
		t.Fatal(err)
	}
	if r.parser != "pflag" || len(r.flags) != 2 || r.flags[0].Kind != "uint" || r.flags[0].Bits != 16 || !r.flags[1].boolLike ||
		r.norm["log_level"] != "log-level" || r.err != "unknown flag: --x" || !reflect.DeepEqual(r.rest, []string{"a b", "c"}) {
		t.Errorf("%+v", r)
	}
	for _, bad := range []string{"", "parser flag\n", "parser flag\nflag \"x\"\nend\n", "parser flag\nerr \"unterminated\nend\n"} {
		if _, err := parseDump([]byte(bad)); err == nil {
			t.Errorf("%q: accepted", bad)
		}
	}
}

func TestNearest(t *testing.T) {
	names := []string{"enable-http2", "health-probe-bind-address", "leader-elect", "metrics-bind-address", "metrics-secure", "log-level", "workers"}
	for in, want := range map[string]string{
		"metrics-addr":           "metrics-bind-address",
		"enable-leader-election": "leader-elect",
		"log_level":              "log-level",
		"worker":                 "workers",
		"log-format":             "",
		"v":                      "",
	} {
		if got := nearest(in, names); got != want {
			t.Errorf("nearest(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestExpand(t *testing.T) {
	vars := map[string]*envVal{"A": {known: true, value: "1"}, "S": {why: "comes from Secret s"}}
	for in, want := range map[string]argTok{
		"--a=$(A)":     {text: "--a=1"},
		"--a=$$(A)":    {text: "--a=$(A)"},
		"--a=$(B)":     {text: "--a=$(B)"},
		"--a=$(S)":     {text: "--a=$(S)", unknown: true, vars: "$(S), which comes from Secret s"},
		"--a=$(A":      {text: "--a=$(A"},
		"$":            {text: "$"},
		"--a=$(A)$(A)": {text: "--a=11"},
	} {
		if got := expand(in, vars, nil); got != want {
			t.Errorf("expand(%q) = %+v, want %+v", in, got, want)
		}
	}
	if got := expand("--a=$(B)", vars, []string{"Secret s"}); !got.unknown {
		t.Errorf("a variable an unreadable envFrom may set must be unknown")
	}
}

func TestPlaceholdersParse(t *testing.T) {
	for _, kind := range []string{"bool", "int", "uint", "float", "duration", "ip", "ipnet", "count", "string"} {
		v, ok := placeholder(kind)
		d := &flagDef{Kind: kind, Bits: 64}
		if !ok || valueErr(d, v) != nil {
			t.Errorf("%s: placeholder %q doesn't parse", kind, v)
		}
	}
	if _, ok := placeholder("custom"); ok {
		t.Errorf("custom flags must not get a placeholder")
	}
}
