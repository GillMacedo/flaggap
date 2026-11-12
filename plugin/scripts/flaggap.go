// Command flaggap checks the command-line arguments that Kubernetes
// manifests pass to a Go program against the flags the program's source
// defines, and replays the parsing rules of the flag package or spf13/pflag.
// It reads files and runs go list; it never connects to a cluster.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"
)

const version = "0.1.0"

const (
	sevNote = iota
	sevWarn
	sevError
)

var sevNames = map[int]string{sevNote: "note", sevWarn: "WARN", sevError: "ERROR"}

type finding struct {
	sev       int
	Severity  string `json:"severity"`
	Code      string `json:"code"`
	Kind      string `json:"kind,omitempty"`
	Namespace string `json:"namespace,omitempty"`
	Name      string `json:"name,omitempty"`
	Container string `json:"container,omitempty"`
	Arg       string `json:"arg,omitempty"`
	Message   string `json:"message"`
	Fix       string `json:"fix,omitempty"`
	File      string `json:"file,omitempty"`
}

// inputError means the input can't be trusted; nothing is reported.
type inputError struct{ msg string }

func (e *inputError) Error() string { return e.msg }

func untrusted(format string, a ...any) error {
	return &inputError{fmt.Sprintf(format, a...)}
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

type report struct {
	Version    string     `json:"version"`
	Program    *program   `json:"program"`
	Flags      int        `json:"flags"`
	Workloads  int        `json:"workloads"`
	Containers int        `json:"containers"`
	Checked    int        `json:"checked"`
	Findings   []*finding `json:"findings"`
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	var direct []string
	directMode := false
	for i, a := range args {
		if a == "--" {
			direct, args, directMode = args[i+1:], args[:i], true
			break
		}
	}
	fl := flag.NewFlagSet("flaggap", flag.ContinueOnError)
	fl.SetOutput(stderr)
	pkg := fl.String("pkg", ".", "the Go main package: a directory, or a package pattern resolved in the current directory")
	ctrs := fl.String("container", "", "comma-separated names of the containers that run the program; needed when the input has more than one container")
	format := fl.String("format", "text", "output format: text or json")
	failOn := fl.String("fail-on", "error", "exit 1 when a finding is at or above this level: error, warn or never")
	list := fl.Bool("list-flags", false, "print the flags the program parses, for the subcommand given after -- if any, and exit")
	timeout := fl.Duration("timeout", 20*time.Second, "how long the program may run before it parses its flags")
	entry := fl.String("entrypoint", "", "space-separated words the image's ENTRYPOINT passes before the container's args, such as a subcommand; used for containers that set args and no command")
	showVersion := fl.Bool("version", false, "print the version")
	fl.Usage = func() {
		fmt.Fprintf(stderr, `usage: flaggap [flags] MANIFEST|DIR|- ...
       flaggap [flags] -- ARG...

Checks the arguments that rendered Kubernetes manifests (or the words after --)
pass to a Go program against the flags its source defines.
Exit 0: nothing at or above --fail-on. Exit 1: findings. Exit 2: input can't be trusted or bad usage.

`)
		fl.PrintDefaults()
	}
	if err := fl.Parse(args); err != nil {
		return 2
	}
	if *showVersion {
		fmt.Fprintln(stdout, "flaggap", version)
		return 0
	}
	if *format != "text" && *format != "json" {
		fmt.Fprintln(stderr, "flaggap: --format must be text or json")
		return 2
	}
	if *failOn != "error" && *failOn != "warn" && *failOn != "never" {
		fmt.Fprintln(stderr, "flaggap: --fail-on must be error, warn or never")
		return 2
	}
	if directMode && fl.NArg() > 0 {
		fmt.Fprintln(stderr, "flaggap: give manifests or arguments after --, not both")
		return 2
	}
	if !directMode && fl.NArg() == 0 && !*list {
		fl.Usage()
		return 2
	}
	if *list && fl.NArg() > 0 {
		fmt.Fprintln(stderr, "flaggap: --list-flags takes no manifests; put a subcommand after --")
		return 2
	}
	if directMode && *ctrs != "" {
		fmt.Fprintln(stderr, "flaggap: --container needs manifests")
		return 2
	}

	// Read the manifests first: they are cheap, and a bad file shouldn't
	// wait for a build.
	var cs []*container
	workloads := 0
	if !directMode && !*list {
		objs, err := load(fl.Args(), stdin)
		if err != nil {
			return refuse(stderr, err)
		}
		all, n, err := containers(objs)
		if err != nil {
			return refuse(stderr, err)
		}
		workloads = n
		cs, err = pick(all, *ctrs)
		if err != nil {
			return refuse(stderr, err)
		}
	}

	dir, pattern := ".", *pkg
	if st, err := os.Stat(*pkg); err == nil && st.IsDir() {
		dir, pattern = *pkg, "."
	}
	if *timeout <= 0 {
		fmt.Fprintln(stderr, "flaggap: --timeout must be positive")
		return 2
	}
	b, err := buildProgram(dir, pattern)
	if err != nil {
		return refuse(stderr, err)
	}
	b.timeout = *timeout
	defer b.cleanup()
	if *list {
		// With a subcommand after --, list the flags it parses.
		r, out, err := b.run(append(append([]string{}, direct...), "--"))
		if err != nil {
			return refuse(stderr, fmt.Errorf("%v. Its output:\n%s", err, clip(out)))
		}
		if os.Getenv("FLAGGAP_DEBUG") != "" {
			fmt.Fprintln(stderr, "parse called from:", strings.Join(r.caller, " <- "))
		}
		printFlags(stdout, r.program(b), *format)
		return 0
	}

	rep := &report{Version: version, Workloads: workloads}
	if directMode {
		var toks []argTok
		for _, a := range direct {
			toks = append(toks, argTok{text: a})
		}
		fs, prog, err := checkArgs(b, toks, where{})
		if err != nil {
			return refuse(stderr, err)
		}
		rep.Checked, rep.Program, rep.Findings = 1, prog, fs
	} else {
		rep.Containers = len(cs)
		var reasons []string
		for _, ct := range cs {
			argv, why := ct.argv(strings.Fields(*entry))
			if why != "" {
				reasons = append(reasons, ct.at.String()+": "+why)
				rep.Findings = append(rep.Findings, &finding{sev: sevNote, Code: "not-checked", Kind: ct.at.Kind, Namespace: ct.at.Namespace, Name: ct.at.Name, Container: ct.at.Container, File: ct.at.File, Message: why})
				continue
			}
			fs, prog, err := checkArgs(b, argv, ct.at)
			if err != nil {
				return refuse(stderr, fmt.Errorf("%s: %w", ct.at, err))
			}
			rep.Checked++
			if rep.Program == nil {
				rep.Program = prog
			}
			rep.Findings = append(rep.Findings, fs...)
		}
		if rep.Checked == 0 {
			return refuse(stderr, untrusted("no container's arguments could be read: %s", strings.Join(reasons, "; ")))
		}
	}
	rep.Flags = len(rep.Program.Flags)
	for _, f := range rep.Findings {
		f.Severity = sevNames[f.sev]
	}
	sort.SliceStable(rep.Findings, func(i, j int) bool { return rep.Findings[i].sev > rep.Findings[j].sev })
	if rep.Findings == nil {
		rep.Findings = []*finding{}
	}
	if *format == "json" {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		enc.Encode(rep)
	} else {
		printText(stdout, rep, directMode)
	}
	limit := map[string]int{"error": sevError, "warn": sevWarn, "never": 99}[*failOn]
	for _, f := range rep.Findings {
		if f.sev >= limit {
			return 1
		}
	}
	return 0
}

func refuse(stderr io.Writer, err error) int {
	fmt.Fprintf(stderr, "flaggap: can't trust the input, nothing was checked: %v\n", err)
	return 2
}

// pick chooses the containers that run the program.
func pick(all []*container, names string) ([]*container, error) {
	var listed []string
	for _, c := range all {
		listed = append(listed, c.at.Container)
	}
	if names == "" {
		uniq := map[string]bool{}
		for _, n := range listed {
			uniq[n] = true
		}
		if len(all) == 1 {
			return all, nil
		}
		var u []string
		for n := range uniq {
			u = append(u, n)
		}
		sort.Strings(u)
		return nil, untrusted("the input has %d containers (%s); name the ones that run the program with --container", len(all), strings.Join(u, ", "))
	}
	var out []*container
	for _, n := range strings.Split(names, ",") {
		n = strings.TrimSpace(n)
		if n == "" {
			continue
		}
		found := false
		for _, c := range all {
			if c.at.Container == n {
				out = append(out, c)
				found = true
			}
		}
		if !found {
			return nil, untrusted("--container %s isn't in the input, which has: %s", n, strings.Join(listed, ", "))
		}
	}
	if len(out) == 0 {
		return nil, untrusted("--container names no containers")
	}
	return out, nil
}

func printText(w io.Writer, rep *report, direct bool) {
	counts := map[int]int{}
	for _, f := range rep.Findings {
		counts[f.sev]++
		at := where{Kind: f.Kind, Namespace: f.Namespace, Name: f.Name, Container: f.Container}
		fmt.Fprintf(w, "%s %s: %s\n", f.Severity, f.Code, at)
		fmt.Fprintf(w, "  %s\n", f.Message)
		if f.Fix != "" {
			fmt.Fprintf(w, "  fix: %s\n", f.Fix)
		}
		if f.File != "" {
			fmt.Fprintf(w, "  in %s\n", f.File)
		}
		fmt.Fprintln(w)
	}
	scope := fmt.Sprintf("%d checked %s", rep.Checked, plural(rep.Checked, "container", "containers"))
	if direct {
		scope = "the arguments"
	}
	parser := "the flag package"
	if rep.Program.Mode == "pflag" {
		parser = "pflag"
	}
	about := fmt.Sprintf("%s: %d %s, parsed with %s.", rep.Program.Path, rep.Flags, plural(rep.Flags, "flag", "flags"), parser)
	if len(rep.Findings) == 0 {
		fmt.Fprintf(w, "No problems in %s. %s\n", scope, about)
		return
	}
	fmt.Fprintf(w, "%d %s, %d %s and %d %s in %s. %s\n",
		counts[sevError], plural(counts[sevError], "error", "errors"),
		counts[sevWarn], plural(counts[sevWarn], "warning", "warnings"),
		counts[sevNote], plural(counts[sevNote], "note", "notes"), scope, about)
}

func printFlags(w io.Writer, prog *program, format string) {
	names := prog.names()
	if format == "json" {
		var out []*flagDef
		for _, n := range names {
			out = append(out, prog.Flags[n])
		}
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		enc.Encode(map[string]any{"program": prog, "flags": out})
		return
	}
	parser := "the flag package"
	if prog.Mode == "pflag" {
		parser = "pflag"
	}
	fmt.Fprintf(w, "%s reads %d flags with %s:\n", prog.Path, len(names), parser)
	for _, n := range names {
		d := prog.Flags[n]
		dash := "-"
		if prog.Mode == "pflag" {
			dash = "--"
		}
		label := dash + n
		if d.Shorthand != "" {
			label += ", -" + d.Shorthand
		}
		extra := ""
		if d.Deprecated != "" {
			extra = " (deprecated)"
		}
		fmt.Fprintf(w, "  %-34s %-9s default %q%s\n", label, d.Kind, d.Default, extra)
	}
}

// checkArgs replays the parser over args with the flag table the program
// reports, and checks every parse failure it predicts against the real
// parser's error. After a confirmed failure it removes those arguments and
// runs the program again, so each failure in the list is a real one.
func checkArgs(b *binary, toks []argTok, at where) ([]*finding, *program, error) {
	var out []*finding
	var first *program
	for iter := 0; iter < 30; iter++ {
		r0, output, err := b.run(texts(toks))
		if err != nil {
			return out, first, b.noParse(err, output, at, &out)
		}
		prog := r0.program(b)
		if first == nil {
			first = prog
		}
		c := &checker{prog: prog, at: at}
		c.check(toks)
		r := r0
		if !equal(c.real, texts(toks)) {
			r, output, err = b.run(c.real)
			if err != nil {
				return out, first, b.noParse(err, output, at, &out)
			}
		}
		if r.partial(c.real) {
			return nil, nil, untrusted("the program's first flag parse read %q of the arguments %q, so another parse reads the rest, which flaggap doesn't see", r.args, c.real)
		}
		for _, f := range c.findings {
			if f.Code == "ignored-after-positional" {
				for _, a := range strings.Fields(f.Arg) {
					if !contains(r.rest, a) {
						return nil, nil, untrusted("flaggap expected the parser to leave %s unparsed, but it didn't; please report this with the arguments", a)
					}
				}
			}
		}
		switch {
		case c.fatal != nil && r.err != "" && strings.Contains(r.err, c.goMsg):
			out = append(out, c.fatal)
			toks = append(append([]argTok{}, toks[:c.fatalAt[0]]...), toks[c.fatalAt[1]:]...)
			continue
		case c.fatal != nil && r.err == "":
			return nil, nil, untrusted("flaggap expected the program to reject %s with %q, but its parser accepted the arguments; please report this with the arguments", c.fatal.Arg, c.goMsg)
		case r.err != "":
			{
				// A value the replay can't judge, such as a custom type: the
				// real error names the flag, so drop it and look further.
				if from, to, ok := c.argsOf(r.err); ok && (c.fatal == nil || from < c.fatalAt[0]) {
					out = append(out, &finding{sev: sevError, Code: "bad-value", Kind: at.Kind, Namespace: at.Namespace, Name: at.Name, Container: at.Container, File: at.File,
						Arg: strings.Join(texts(toks[from:to]), " "), Message: fmt.Sprintf("the program's parser rejects %s with %q, so the program exits at startup", strings.Join(texts(toks[from:to]), " "), r.err)})
					toks = append(append([]argTok{}, toks[:from]...), toks[to:]...)
					continue
				}
			}
			for _, f := range c.findings {
				if f != c.fatal {
					out = append(out, f)
				}
			}
			out = append(out, &finding{sev: sevError, Code: "parse-error", Kind: at.Kind, Namespace: at.Namespace, Name: at.Name, Container: at.Container, File: at.File,
				Message: fmt.Sprintf("the program's parser rejects the arguments with %q, so the program exits at startup", r.err)})
			return out, first, nil
		default:
			out = append(out, c.findings...)
			if pos := positionals(r.rest, out); len(pos) > 0 && r.fromCobra() {
				out = append(out, &finding{sev: sevNote, Code: "positional", Kind: at.Kind, Namespace: at.Namespace, Name: at.Name, Container: at.Container, File: at.File,
					Arg: strings.Join(pos, " "), Message: positionalMsg(pos, r.parser)})
			}
			return out, first, nil
		}
	}
	return nil, nil, untrusted("more than 30 parse failures; fix the first ones and run again")
}

// noParse turns a run that ended before the program parsed its flags into
// a finding (cobra's unknown command) or a refusal.
func (b *binary) noParse(err error, output string, at where, out *[]*finding) error {
	for _, line := range strings.Split(output, "\n") {
		if k := strings.Index(line, "unknown command \""); k >= 0 {
			*out = append(*out, &finding{sev: sevError, Code: "unknown-command", Kind: at.Kind, Namespace: at.Namespace, Name: at.Name, Container: at.Container, File: at.File,
				Message: fmt.Sprintf("the program exits at startup with %q before it parses any flags", strings.TrimSpace(strings.TrimPrefix(line, "Error: ")))})
			return nil
		}
	}
	return untrusted("%v, so its flags couldn't be read. Its output:\n%s", err, clip(output))
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func contains(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

// positionals lists the arguments the real parser left over that no
// finding already explains, such as the false of --debug false.
func positionals(rest []string, fs []*finding) []string {
	explained := map[string]bool{}
	for _, f := range fs {
		if f.Code == "ignored-after-positional" || f.Code == "after-positional" {
			return nil
		}
		for _, w := range strings.Fields(f.Arg) {
			explained[w] = true
		}
	}
	var out []string
	for _, r := range rest {
		if !explained[r] {
			out = append(out, r)
		}
	}
	return out
}

func quoteAll(l []string) []string {
	var out []string
	for _, s := range l {
		out = append(out, fmt.Sprintf("%q", s))
	}
	return out
}

func positionalMsg(pos []string, parser string) string {
	return fmt.Sprintf("the command gets the positional %s %s, and cobra checks positional arguments with the command's Args after it parses flags, where flaggap stops", plural(len(pos), "argument", "arguments"), strings.Join(quoteAll(pos), " "))
}
