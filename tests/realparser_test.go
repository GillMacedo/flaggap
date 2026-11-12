package tests

import (
	"bytes"
	"encoding/json"
	"math/rand"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
)

// target is a test program, the words its arguments are made of, and how
// to tell that it parsed its flags and went on to run.
type target struct {
	name   string
	pkg    string // --pkg, relative to the repository root
	prefix []string
	vocab  []string
	ok     func(out []byte) bool
	// leftovers reads the positional arguments the program saw
	leftovers func(out []byte) []string
}

func jsonArgs(out []byte) []string {
	var v struct{ Args []string }
	json.Unmarshal(out, &v)
	return v.Args
}

var targets = []target{
	{
		name: "stdprog", pkg: "./tests/testdata/stdprog",
		vocab: []string{"-debug", "--debug", "-debug=false", "-debug=yes", "false", "true", "-workers=8", "-workers", "8",
			"-workers=x", "-max-bytes=1k", "-retries=-1", "-seed=0x10", "-ratio=1e3", "-timeout=30", "-timeout=30s",
			"-listen=:9090", "-listen", "-log-level=debug", "-log-level=loud", "-trace", "-trace=maybe", "-tag=a", "-pretty",
			"-bind-ip=10.0.0.1", "-bind-ip=nope", "-nope", "--nope=1", "serve", "--", "-", "---x", "-=x", "-h", "--help"},
		ok:        func(out []byte) bool { return bytes.HasPrefix(out, []byte("{")) },
		leftovers: jsonArgs,
	},
	{
		name: "plain", pkg: "tests/testdata/pflagmod/plain",
		vocab: []string{"--debug", "-d", "--debug=false", "false", "-dw3", "-w", "3", "--workers=x", "-workers=3", "--nice=200",
			"--port=70000", "-p8080", "-port=80", "--ratio=1.5", "-t5s", "--timeout=5", "--name", "x", "-n=y", "-vvv", "--verbose=2",
			"--tags=a,b", "--bind=1.2.3.4", "--bind=nope", "--allow=10.1.0.0/16", "--allow=bad", "--labels=a=b", "--labels=bad",
			"--threads=2", "--nope", "-x", "serve", "--", "---x", "--=x", "-h", "--help", "-dv", "-name=z", "-v", "2"},
		ok:        func(out []byte) bool { return bytes.HasPrefix(out, []byte("{")) },
		leftovers: jsonArgs,
	},
	{
		name: "strict", pkg: "tests/testdata/pflagmod/strict",
		vocab: []string{"--port=1", "--port", "2", "--debug", "false", "serve", "--nope", "--debug=true"},
		ok: func(out []byte) bool {
			return len(out) > 0 && out[0] >= '0' && out[0] <= '9'
		},
		leftovers: func(out []byte) []string {
			s := string(out)
			i, j := strings.Index(s, "["), strings.LastIndex(s, "]")
			if i < 0 || j < i {
				return nil
			}
			return strings.Fields(s[i+1 : j])
		},
	},
	{
		name: "worker", pkg: "examples/worker", prefix: []string{"run"},
		vocab: []string{"-q", "jobs", "--queue=a", "-c", "8", "-concurrency=8", "--poll-interval=30", "--poll-interval=1m",
			"--dry-run", "false", "--dry-run=nope", "--metrics-addr=:1", "--threads=3", "-v", "2", "-v=x", "--log_level=debug",
			"--log-level", "--help", "-h", "extra"},
		ok: func(out []byte) bool { return bytes.HasPrefix(out, []byte("run ")) },
		leftovers: func(out []byte) []string {
			s := string(out)
			i := strings.Index(s, "args=[")
			if i < 0 {
				return nil
			}
			return strings.Fields(strings.TrimSuffix(strings.TrimSpace(s[i+6:]), "]"))
		},
	},
}

var fatalCodes = map[string]bool{"unknown-flag": true, "bad-value": true, "missing-value": true, "bad-syntax": true,
	"parse-error": true, "exits": true, "unknown-command": true}

// TestAgainstRealParsers gives each test program random argument lists and
// checks flaggap against what the program really does: when the program
// fails to start, flaggap reports an error; when it starts, flaggap reports
// no parse failure; and when the program leaves a flag unparsed, flaggap
// reports an error. flaggap must never refuse these programs.
func TestAgainstRealParsers(t *testing.T) {
	n := 30
	if s := os.Getenv("FLAGGAP_RANDOM_RUNS"); s != "" {
		n, _ = strconv.Atoi(s)
	}
	if testing.Short() {
		n = 8
	}
	for _, tg := range targets {
		tg := tg
		t.Run(tg.name, func(t *testing.T) {
			t.Parallel()
			rnd := rand.New(rand.NewSource(int64(len(tg.name)) * 7919))
			for k := 0; k < n; k++ {
				args := append([]string{}, tg.prefix...)
				for j := 1 + rnd.Intn(6); j > 0; j-- {
					args = append(args, tg.vocab[rnd.Intn(len(tg.vocab))])
				}
				compare(t, tg, args)
			}
		})
	}
}

func compare(t *testing.T, tg target, args []string) jsonReport {
	t.Helper()
	out, _ := exec.Command(plain[tg.name], args...).Output()
	started := tg.ok(out)
	t.Logf("%s %q started=%v", tg.name, args, started)
	r := runTool(t, nil, append([]string{"--format", "json", "--fail-on", "never", "--pkg", tg.pkg, "--"}, args...)...)
	if r.code != 0 {
		t.Errorf("%q: flaggap exit %d: %s", args, r.code, r.stderr)
		return jsonReport{}
	}
	var rep jsonReport
	if err := json.Unmarshal([]byte(r.stdout), &rep); err != nil {
		t.Fatalf("%q: %v", args, err)
	}
	fatal := 0
	for _, f := range rep.Findings {
		if fatalCodes[f.Code] {
			fatal++
		}
	}
	switch {
	case !started && errorsIn(rep) == 0:
		t.Errorf("%q: the program didn't start (%q) but flaggap reported no error: %v", args, out, codes(rep))
	case started && fatal > 0:
		t.Errorf("%q: the program started but flaggap reported a parse failure: %v", args, codes(rep))
	case started:
		afterDash := false
		for _, a := range args {
			if a == "--" {
				afterDash = true
			}
		}
		for _, l := range tg.leftovers(out) {
			if !afterDash && len(l) > 1 && l[0] == '-' && errorsIn(rep) == 0 {
				t.Errorf("%q: the program left %s unparsed but flaggap reported no error", args, l)
			}
		}
	}
	return rep
}

// TestRealParserCases pins cases the random test may not draw.
func TestRealParserCases(t *testing.T) {
	cases := map[string][][]string{
		"stdprog": {{"-debug", "false", "-workers=2"}, {"-trace", "false"}, {"-listen", "-workers=3"}, {"-workers", "-1"},
			{"-workers=1", "-workers=2"}, {"-max-bytes=9223372036854775808"}, {"-bind-ip=nope"}, {"-log-level=loud", "-nope"}},
		"plain": {{"--debug", "false"}, {"-port=80"}, {"-name=z"}, {"-dvx"}, {"--name", "--debug"}, {"--threads=1"}, {"-pv"},
			{"-v", "3"}, {"--verbose", "2", "-d"}, {"serve", "-v"}},
		"strict": {{"serve", "--port=1"}, {"--debug", "false", "--port=2"}},
		"worker": {{"run", "-concurrency=8"}, {"run", "--dry-run", "false"}, {"run", "--log_level=debug"}, {"run", "-v=x"}},
	}
	for _, tg := range targets {
		for _, args := range cases[tg.name] {
			compare(t, tg, append(append([]string{}, tg.prefix...), args...))
		}
		switch tg.name {
		case "plain":
			t.Run("reports the first unknown shorthand", func(t *testing.T) {
				rep := compare(t, tg, []string{"-dvx"})
				if len(rep.Findings) != 1 || rep.Findings[0].Code != "unknown-flag" || !strings.Contains(rep.Findings[0].Message, "unknown shorthand flag: 'x'") {
					t.Fatalf("got %+v, want an unknown shorthand finding for x", rep.Findings)
				}
			})
		case "worker":
			t.Run("suggests the normalized long flag", func(t *testing.T) {
				rep := compare(t, tg, []string{"run", "-log_level=debug"})
				if len(rep.Findings) != 1 || rep.Findings[0].Code != "single-dash" || rep.Findings[0].Fix != "write --log-level=debug" {
					t.Fatalf("got %+v, want a single-dash finding with the normalized flag and its value", rep.Findings)
				}
			})
			t.Run("accepts the double-dash flag", func(t *testing.T) {
				rep := compare(t, tg, []string{"run", "--log-level=debug"})
				if len(rep.Findings) != 0 {
					t.Fatalf("got %+v, want no findings", rep.Findings)
				}
			})
		}
	}
}
