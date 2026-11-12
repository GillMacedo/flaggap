package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

const pflagPk = "github.com/spf13/pflag"

// Command-line parsers flaggap doesn't model. A program whose own module
// imports one is refused, because its flags would all look undefined.
var otherParsers = []string{
	"github.com/alecthomas/kingpin",
	"gopkg.in/alecthomas/kingpin",
	"github.com/alecthomas/kong",
	"github.com/urfave/cli",
	"gopkg.in/urfave/cli",
	"github.com/jessevdk/go-flags",
	"github.com/peterbourgon/ff",
	"github.com/docopt/docopt-go",
	"github.com/integrii/flaggy",
	"github.com/alexflint/go-arg",
	"github.com/mitchellh/cli",
}

// flagDef is one flag in the set the program parses.
type flagDef struct {
	Name       string `json:"name"`
	Shorthand  string `json:"shorthand,omitempty"`
	Kind       string `json:"kind"` // bool, int, uint, float, duration, string, ip, ipnet, count, boolfunc, list, custom
	Bits       int    `json:"-"`
	Default    string `json:"default"`
	Deprecated string `json:"deprecated,omitempty"`
	boolLike   bool   // takes no separate value argument
	scalar     bool   // a second occurrence replaces the first
}

// program is the flag table and parser behavior seen in one run.
type program struct {
	Path        string              `json:"package"`
	Mode        string              `json:"parser"` // "flag" or "pflag"
	Subcommands bool                `json:"-"`      // flag mode: the module creates its own FlagSets
	Flags       map[string]*flagDef `json:"-"`
	Shorthands  map[string]*flagDef `json:"-"`
	norm        map[string]string   // pflag: argument name -> name after the set's normalize func
	interOff    bool                // pflag: SetInterspersed(false)
}

// binary is the program built with the flag hooks.
type binary struct {
	path        string
	pkgPath     string
	subcommands bool
	parses      bool // the main module imports flag, pflag or cobra
	tmp         string
	timeout     time.Duration
}

type listPkg struct {
	ImportPath string
	Name       string
	Dir        string
	GoFiles    []string
	Standard   bool
	DepOnly    bool
	Imports    []string
	Module     *struct {
		Path    string
		Main    bool
		Dir     string
		GoMod   string
		Replace *struct{ Path string }
	}
	Error      *struct{ Err string }
	DepsErrors []*struct{ Err string }
	Incomplete bool
}

// buildProgram builds the main package in dir with flag and pflag patched
// so the program reports its flag set when it parses its arguments.
func buildProgram(dir, pattern string) (*binary, error) {
	cmd := exec.Command("go", "list", "-deps", "-json", "--", pattern)
	cmd.Dir = dir
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return nil, untrusted("go list %s in %s failed, so the program can't be built (%v):\n%s", pattern, dir, err, clip(errb.String()))
	}
	var pkgs []*listPkg
	dec := json.NewDecoder(&out)
	for {
		p := new(listPkg)
		if err := dec.Decode(p); err == io.EOF {
			break
		} else if err != nil {
			return nil, untrusted("reading go list output: %v", err)
		}
		pkgs = append(pkgs, p)
	}
	var root *listPkg
	byPath := map[string]*listPkg{}
	for _, p := range pkgs {
		byPath[p.ImportPath] = p
		if p.Error != nil {
			return nil, untrusted("package %s: %s", p.ImportPath, p.Error.Err)
		}
		if len(p.DepsErrors) > 0 {
			return nil, untrusted("package %s: %s", p.ImportPath, p.DepsErrors[0].Err)
		}
		if p.Incomplete {
			return nil, untrusted("package %s is incomplete", p.ImportPath)
		}
		if !p.DepOnly {
			if root != nil {
				return nil, untrusted("%s matches more than one package; name one main package, such as ./cmd/server", pattern)
			}
			root = p
		}
	}
	if root == nil {
		return nil, untrusted("go list %s printed no packages", pattern)
	}
	if root.Name != "main" {
		return nil, untrusted("%s is package %s, not a main package; name the program's main package with --pkg", root.ImportPath, root.Name)
	}
	b := &binary{pkgPath: root.ImportPath}
	for _, p := range pkgs {
		if p.Module == nil || !p.Module.Main {
			continue
		}
		for _, imp := range p.Imports {
			if imp == "flag" || imp == pflagPk || imp == "github.com/spf13/cobra" {
				b.parses = true
			}
			for _, o := range otherParsers {
				if imp == o || strings.HasPrefix(imp, o+"/") {
					return nil, untrusted("package %s imports %s, a command-line parser flaggap doesn't read; it only knows the flag package, spf13/pflag and spf13/cobra", p.ImportPath, imp)
				}
			}
		}
		for _, f := range p.GoFiles {
			src, err := os.ReadFile(filepath.Join(p.Dir, f))
			if err != nil {
				return nil, untrusted("%v", err)
			}
			if bytes.Contains(src, []byte("flag.NewFlagSet(")) {
				b.subcommands = true
			}
		}
	}

	if !b.parses {
		return nil, untrusted("no package in %s's own module imports flag, %s or github.com/spf13/cobra, so flaggap can't tell that it parses its arguments with them, and won't run it", root.ImportPath, pflagPk)
	}

	tmp, err := os.MkdirTemp("", "flaggap-")
	if err != nil {
		return nil, err
	}
	b.tmp = tmp
	overlay := map[string]string{}
	patch := func(p *listPkg, hook string) error {
		file := filepath.Join(p.Dir, "flag.go")
		src, err := os.ReadFile(file)
		if err != nil {
			return untrusted("reading %s: %v", file, err)
		}
		patched, n := patchParse(string(src))
		if n == 0 {
			return untrusted("%s has no (*FlagSet).Parse method flaggap knows how to hook; this version of %s isn't supported", file, p.ImportPath)
		}
		name := strings.ReplaceAll(p.ImportPath, "/", "_")
		pf := filepath.Join(tmp, name+"_flag.go")
		hf := filepath.Join(tmp, name+"_hook.go")
		if err := os.WriteFile(pf, []byte(patched), 0o600); err != nil {
			return err
		}
		if err := os.WriteFile(hf, []byte(hook), 0o600); err != nil {
			return err
		}
		overlay[file] = pf
		overlay[filepath.Join(p.Dir, "zz_flaggap_hook.go")] = hf
		return nil
	}
	fp := byPath["flag"]
	if fp == nil {
		b.cleanup()
		return nil, untrusted("%s doesn't link the flag package, so it can't parse flags with flag, pflag or cobra", root.ImportPath)
	}
	if err := patch(fp, stdHook); err != nil {
		b.cleanup()
		return nil, err
	}
	var extraArgs, extraEnv []string
	if pp := byPath[pflagPk]; pp != nil {
		if !strings.HasPrefix(pp.Dir, modCache()+string(filepath.Separator)) {
			if err := patch(pp, pflagHook); err != nil {
				b.cleanup()
				return nil, err
			}
		} else {
			// go build won't overlay files in the module cache, so build
			// against a patched copy with a replace in a copy of go.mod.
			args, env, err := copyPflag(tmp, dir, pp, root)
			if err != nil {
				b.cleanup()
				return nil, err
			}
			extraArgs, extraEnv = args, env
		}
	}
	ov, _ := json.Marshal(map[string]any{"Replace": overlay})
	ovFile := filepath.Join(tmp, "overlay.json")
	if err := os.WriteFile(ovFile, ov, 0o600); err != nil {
		b.cleanup()
		return nil, err
	}
	b.path = filepath.Join(tmp, "program")
	bargs := append([]string{"build", "-overlay", ovFile, "-o", b.path}, extraArgs...)
	build := exec.Command("go", append(bargs, "--", pattern)...)
	build.Dir = dir
	build.Env = append(os.Environ(), extraEnv...)
	var berr bytes.Buffer
	build.Stdout, build.Stderr = &berr, &berr
	if err := build.Run(); err != nil {
		b.cleanup()
		return nil, untrusted("go build %s failed (%v):\n%s", pattern, err, clip(berr.String()))
	}
	return b, nil
}

func modCache() string {
	out, err := exec.Command("go", "env", "GOMODCACHE").Output()
	if err != nil {
		return "\x00"
	}
	return filepath.Clean(strings.TrimSpace(string(out)))
}

// copyPflag copies the pflag module out of the module cache, patches it,
// and returns build flags and environment that replace pflag with the copy:
// a copy of go.mod with a replace, or in workspace mode a copy of go.work.
func copyPflag(tmp, dir string, pp, root *listPkg) ([]string, []string, error) {
	if pp.Module == nil || pp.Module.Dir == "" || root.Module == nil || root.Module.GoMod == "" {
		return nil, nil, untrusted("can't find the modules of %s and %s", pflagPk, root.ImportPath)
	}
	if pp.Module.Replace != nil {
		return nil, nil, untrusted("%s is replaced by a module in the module cache, which flaggap can't hook; vendor it with go mod vendor and run again", pflagPk)
	}
	dst := filepath.Join(tmp, "pflag")
	err := filepath.WalkDir(pp.Module.Dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(pp.Module.Dir, p)
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o700)
		}
		if !d.Type().IsRegular() {
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o600)
	})
	if err != nil {
		return nil, nil, untrusted("copying %s: %v", pflagPk, err)
	}
	rel, _ := filepath.Rel(pp.Module.Dir, pp.Dir)
	pkgDir := filepath.Join(dst, rel)
	file := filepath.Join(pkgDir, "flag.go")
	src, err := os.ReadFile(file)
	if err != nil {
		return nil, nil, untrusted("reading %s: %v", file, err)
	}
	patched, n := patchParse(string(src))
	if n == 0 {
		return nil, nil, untrusted("%s has no (*FlagSet).Parse method flaggap knows how to hook; this version of %s isn't supported", file, pflagPk)
	}
	if err := os.WriteFile(file, []byte(patched), 0o600); err != nil {
		return nil, nil, err
	}
	if err := os.WriteFile(filepath.Join(pkgDir, "zz_flaggap_hook.go"), []byte(pflagHook), 0o600); err != nil {
		return nil, nil, err
	}
	if _, err := os.Stat(filepath.Join(dst, "go.mod")); err != nil {
		if err := os.WriteFile(filepath.Join(dst, "go.mod"), []byte("module "+pp.Module.Path+"\n"), 0o600); err != nil {
			return nil, nil, err
		}
	}
	gowork := goEnvIn(dir, "GOWORK")
	if gowork != "" && gowork != "off" {
		return workspaceReplace(tmp, dir, gowork, pp.Module.Path, dst)
	}
	gomod, err := os.ReadFile(root.Module.GoMod)
	if err != nil {
		return nil, nil, untrusted("reading %s: %v", root.Module.GoMod, err)
	}
	mf := filepath.Join(tmp, "go.mod")
	gomod = append(gomod, []byte("\nreplace "+pp.Module.Path+" => "+dst+"\n")...)
	if err := os.WriteFile(mf, gomod, 0o600); err != nil {
		return nil, nil, err
	}
	if sum, err := os.ReadFile(strings.TrimSuffix(root.Module.GoMod, ".mod") + ".sum"); err == nil {
		if err := os.WriteFile(filepath.Join(tmp, "go.sum"), sum, 0o600); err != nil {
			return nil, nil, err
		}
	}
	return []string{"-modfile", mf}, nil, nil
}

func goEnvIn(dir, name string) string {
	cmd := exec.Command("go", "env", name)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// workspaceReplace writes a copy of go.work with absolute use paths and a
// replace for pflag.
func workspaceReplace(tmp, dir, gowork, modPath, dst string) ([]string, []string, error) {
	cmd := exec.Command("go", "work", "edit", "-json", gowork)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return nil, nil, untrusted("reading %s: %v", gowork, err)
	}
	var w struct {
		Go        string
		Toolchain string
		Use       []struct{ DiskPath string }
		Replace   []struct {
			Old struct{ Path, Version string }
			New struct{ Path, Version string }
		}
	}
	if err := json.Unmarshal(out, &w); err != nil {
		return nil, nil, untrusted("reading %s: %v", gowork, err)
	}
	base := filepath.Dir(gowork)
	var b strings.Builder
	if w.Go != "" {
		b.WriteString("go " + w.Go + "\n")
	}
	if w.Toolchain != "" {
		b.WriteString("toolchain " + w.Toolchain + "\n")
	}
	for _, u := range w.Use {
		p := u.DiskPath
		if !filepath.IsAbs(p) {
			p = filepath.Join(base, p)
		}
		b.WriteString("use " + strconv.Quote(p) + "\n")
	}
	for _, r := range w.Replace {
		if r.Old.Path == modPath {
			return nil, nil, untrusted("%s replaces %s, which flaggap can't hook", gowork, modPath)
		}
		old := r.Old.Path
		if r.Old.Version != "" {
			old += " " + r.Old.Version
		}
		nw := r.New.Path
		if r.New.Version != "" {
			nw += " " + r.New.Version
		} else if !filepath.IsAbs(nw) && (strings.HasPrefix(nw, "./") || strings.HasPrefix(nw, "../")) {
			nw = filepath.Join(base, nw)
		}
		b.WriteString("replace " + old + " => " + nw + "\n")
	}
	b.WriteString("replace " + modPath + " => " + dst + "\n")
	wf := filepath.Join(tmp, "go.work")
	if err := os.WriteFile(wf, []byte(b.String()), 0o600); err != nil {
		return nil, nil, err
	}
	if sum, err := os.ReadFile(gowork + ".sum"); err == nil {
		if err := os.WriteFile(wf+".sum", sum, 0o600); err != nil {
			return nil, nil, err
		}
	}
	return nil, []string{"GOWORK=" + wf}, nil
}

func (b *binary) cleanup() {
	if b.tmp != "" {
		os.RemoveAll(b.tmp)
	}
}

func clip(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 1500 {
		s = s[:1500] + "..."
	}
	return s
}

// patchParse renames (*FlagSet).Parse and ParseAll and puts a hook in front.
func patchParse(src string) (string, int) {
	n := 0
	for _, m := range []struct{ sig, call, rename string }{
		{"func (f *FlagSet) Parse(arguments []string) error {", "f.flaggapParse(arguments)", "func (f *FlagSet) flaggapParse(arguments []string) error {"},
		{"func (f *FlagSet) ParseAll(arguments []string, fn func(flag *Flag, value string) error) error {", "f.flaggapParseAll(arguments, fn)", "func (f *FlagSet) flaggapParseAll(arguments []string, fn func(flag *Flag, value string) error) error {"},
	} {
		if strings.Count(src, m.sig) != 1 {
			continue
		}
		n++
		src = strings.Replace(src, m.sig, m.sig+"\n\tflaggapHook(f, arguments)\n\treturn "+m.call+"\n}\n\n"+m.rename, 1)
	}
	return src, n
}

// runResult is what one run of the program reported.
type runResult struct {
	parser   string
	flags    []*flagDef
	norm     map[string]string
	interOff bool
	err      string
	rest     []string
	set      []string
	caller   []string // the functions that called Parse, for debugging
	args     []string // the arguments Parse got
}

// fromCobra reports whether cobra called the hooked Parse.
func (r *runResult) fromCobra() bool {
	for _, c := range r.caller {
		if strings.HasSuffix(c, "spf13/cobra.(*Command).ParseFlags") {
			return true
		}
	}
	return false
}

// partial reports whether the hooked Parse got only part of the arguments
// the program was started with, so a later Parse reads the rest.
func (r *runResult) partial(given []string) bool {
	if r.fromCobra() {
		return false
	}
	got := r.args
	if len(got) == len(given)+1 {
		got = got[1:] // Parse(os.Args), with the program name
	}
	if len(got) != len(given) {
		return true
	}
	for i := range got {
		if got[i] != given[i] {
			return true
		}
	}
	return false
}

// run starts the program with args, in an empty directory with an
// almost empty environment, and reads what the hook wrote.
func (b *binary) run(args []string) (*runResult, string, error) {
	work, err := os.MkdirTemp(b.tmp, "run-")
	if err != nil {
		return nil, "", err
	}
	defer os.RemoveAll(work)
	dump := filepath.Join(work, "dump")
	ctx, cancel := context.WithTimeout(context.Background(), b.timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, b.path, args...)
	cmd.Dir = work
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + work, "TMPDIR=" + work, "FLAGGAP_DUMP=" + dump}
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	runErr := cmd.Run()
	data, rerr := os.ReadFile(dump)
	if rerr != nil {
		why := fmt.Sprintf("exited (%v)", runErr)
		if runErr == nil {
			why = "exited with status 0"
		}
		if ctx.Err() != nil {
			why = fmt.Sprintf("was still running after %s", b.timeout)
		}
		return nil, out.String(), fmt.Errorf("%s %s before it parsed its flags", b.pkgPath, why)
	}
	r, err := parseDump(data)
	return r, out.String(), err
}

func parseDump(data []byte) (*runResult, error) {
	r := &runResult{norm: map[string]string{}}
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 1<<20), 1<<24)
	complete := false
	for sc.Scan() {
		f, err := fields(sc.Text())
		if err != nil || len(f) == 0 {
			return nil, fmt.Errorf("unreadable hook output line %q", sc.Text())
		}
		switch f[0] {
		case "parser":
			r.parser = f[1]
		case "flag":
			if len(f) != 7 {
				return nil, fmt.Errorf("unreadable hook flag line %q", sc.Text())
			}
			d := &flagDef{Name: f[1], Shorthand: f[2], Default: f[5], Deprecated: f[6]}
			kind := f[3]
			if k := strings.IndexAny(kind, "0123456789"); k > 0 && (strings.HasPrefix(kind, "int") || strings.HasPrefix(kind, "uint") || strings.HasPrefix(kind, "float")) {
				d.Bits, _ = strconv.Atoi(kind[k:])
				kind = kind[:k]
			}
			setKind(d, kind)
			d.boolLike = f[4] == "true"
			r.flags = append(r.flags, d)
		case "norm":
			r.norm[f[1]] = f[2]
		case "interspersed":
			r.interOff = f[1] == "false"
		case "err":
			r.err = f[1]
		case "rest":
			r.rest = f[1:]
		case "set":
			r.set = f[1:]
		case "caller":
			r.caller = f[1:]
		case "args":
			r.args = f[1:]
		case "end":
			complete = true
		}
	}
	if !complete || r.parser == "" {
		return nil, fmt.Errorf("the hook's output is incomplete")
	}
	return r, nil
}

// fields splits a hook line into bare words and Go-quoted strings.
func fields(line string) ([]string, error) {
	var out []string
	for {
		line = strings.TrimLeft(line, " ")
		if line == "" {
			return out, nil
		}
		if line[0] == '"' {
			q, err := strconv.QuotedPrefix(line)
			if err != nil {
				return nil, err
			}
			s, _ := strconv.Unquote(q)
			out = append(out, s)
			line = line[len(q):]
			continue
		}
		k := strings.IndexByte(line, ' ')
		if k < 0 {
			k = len(line)
		}
		out = append(out, line[:k])
		line = line[k:]
	}
}

func setKind(d *flagDef, kind string) {
	d.Kind, d.scalar = kind, true
	switch kind {
	case "int", "uint", "float":
		if d.Bits == 0 {
			d.Bits = 64
		}
	case "bool", "duration", "string", "ip", "ipnet":
	case "count", "boolfunc", "list":
		d.scalar = false
	default:
		d.Kind, d.scalar = "custom", false
	}
}

func (r *runResult) program(b *binary) *program {
	p := &program{Path: b.pkgPath, Mode: r.parser, Flags: map[string]*flagDef{}, Shorthands: map[string]*flagDef{}, norm: r.norm, interOff: r.interOff}
	p.Subcommands = r.parser == "flag" && b.subcommands
	for _, d := range r.flags {
		p.Flags[d.Name] = d
		if d.Shorthand != "" {
			p.Shorthands[d.Shorthand] = d
		}
	}
	return p
}

func (p *program) names() []string {
	var out []string
	for n := range p.Flags {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// The hooks are added to the flag and pflag packages with go build
// -overlay. They act only when FLAGGAP_DUMP is set, on the first Parse
// that reads the program's own arguments: they write the flag set, run the
// real parser with errors returned instead of printed, and exit.

const hookCommon = `
var flaggapDone bool

// flaggapFromArgs reports whether args came from the command line: cobra
// is parsing, the program has no arguments at all, or args is a non-empty
// subsequence of os.Args (some programs parse os.Args with the name).
func flaggapFromArgs(args []string) bool {
	pcs := make([]uintptr, 32)
	n := runtime.Callers(3, pcs)
	frames := runtime.CallersFrames(pcs[:n])
	for {
		fr, more := frames.Next()
		if strings.HasSuffix(fr.Function, "spf13/cobra.(*Command).ParseFlags") {
			return true
		}
		if !more {
			break
		}
	}
	if len(os.Args) <= 1 {
		return true
	}
	if len(args) == 0 {
		return false
	}
	i := 0
	for _, a := range os.Args {
		if i < len(args) && args[i] == a {
			i++
		}
	}
	return i == len(args)
}

func flaggapQuote(b *strings.Builder, words ...string) {
	for _, w := range words {
		b.WriteString(" ")
		b.WriteString(strconv.Quote(w))
	}
}

func flaggapWrite(path string, b *strings.Builder) {
	pcs := make([]uintptr, 16)
	n := runtime.Callers(3, pcs)
	frames := runtime.CallersFrames(pcs[:n])
	b.WriteString("caller")
	for {
		fr, more := frames.Next()
		flaggapQuote(b, fr.Function)
		if !more {
			break
		}
	}
	b.WriteString("\nend\n")
	if err := os.WriteFile(path, []byte(b.String()), 0600); err != nil {
		os.Exit(3)
	}
	os.Exit(0)
}
`

const stdHook = `package flag

import (
	"io"
	"os"
	"runtime"
	"strconv"
	"strings"
)
` + hookCommon + `
func flaggapHook(f *FlagSet, arguments []string) {
	path := os.Getenv("FLAGGAP_DUMP")
	if path == "" || flaggapDone || !flaggapFromArgs(arguments) {
		return
	}
	flaggapDone = true
	// The real parse below may call the other package's Parse; only
	// this hook reports.
	os.Unsetenv("FLAGGAP_DUMP")
	var b strings.Builder
	b.WriteString("parser flag\n")
	f.VisitAll(func(fl *Flag) {
		kind, boolLike := "custom", false
		switch fl.Value.(type) {
		case *boolValue:
			kind, boolLike = "bool", true
		case *intValue:
			kind = "int" + strconv.Itoa(strconv.IntSize)
		case *int64Value:
			kind = "int64"
		case *uintValue:
			kind = "uint" + strconv.Itoa(strconv.IntSize)
		case *uint64Value:
			kind = "uint64"
		case *float64Value:
			kind = "float64"
		case *durationValue:
			kind = "duration"
		case *stringValue:
			kind = "string"
		case boolFuncValue:
			kind, boolLike = "boolfunc", true
		default:
			if bf, ok := fl.Value.(boolFlag); ok && bf.IsBoolFlag() {
				boolLike = true
			}
		}
		b.WriteString("flag")
		flaggapQuote(&b, fl.Name, "", kind, strconv.FormatBool(boolLike), fl.DefValue, "")
		b.WriteString("\n")
	})
	b.WriteString("args")
	flaggapQuote(&b, arguments...)
	b.WriteString("\n")
	f.errorHandling = ContinueOnError
	f.SetOutput(io.Discard)
	f.Usage = func() {}
	if err := f.flaggapParse(arguments); err != nil {
		b.WriteString("err")
		flaggapQuote(&b, err.Error())
		b.WriteString("\n")
	}
	b.WriteString("rest")
	flaggapQuote(&b, f.Args()...)
	b.WriteString("\nset")
	f.Visit(func(fl *Flag) { flaggapQuote(&b, fl.Name) })
	b.WriteString("\n")
	flaggapWrite(path, &b)
}
`

const pflagHook = `package pflag

import (
	"io"
	"os"
	"reflect"
	"runtime"
	"strconv"
	"strings"
)
` + hookCommon + `
func flaggapHook(f *FlagSet, arguments []string) {
	path := os.Getenv("FLAGGAP_DUMP")
	if path == "" || flaggapDone || !flaggapFromArgs(arguments) {
		return
	}
	flaggapDone = true
	// The real parse below may call the other package's Parse; only
	// this hook reports.
	os.Unsetenv("FLAGGAP_DUMP")
	var b strings.Builder
	b.WriteString("parser pflag\n")
	own := reflect.TypeOf(f).Elem().PkgPath()
	f.VisitAll(func(fl *Flag) {
		kind := "custom"
		if t := reflect.TypeOf(fl.Value); t != nil && t.Kind() == reflect.Ptr && t.Elem().PkgPath() == own {
			switch typ := fl.Value.Type(); typ {
			case "bool", "duration", "string", "ip", "count", "int8", "int16", "int32", "int64",
				"uint8", "uint16", "uint32", "uint64", "float32", "float64":
				kind = typ
			case "int", "uint":
				kind = typ + strconv.Itoa(strconv.IntSize)
			case "ipNet":
				kind = "ipnet"
			default:
				if strings.HasSuffix(typ, "Slice") || strings.HasSuffix(typ, "Array") || strings.HasPrefix(typ, "stringTo") {
					kind = "list"
				}
			}
		}
		b.WriteString("flag")
		flaggapQuote(&b, fl.Name, fl.Shorthand, kind, strconv.FormatBool(fl.NoOptDefVal != ""), fl.DefValue, fl.Deprecated)
		b.WriteString("\n")
	})
	nf := f.GetNormalizeFunc()
	for _, a := range arguments {
		if strings.HasPrefix(a, "--") && len(a) > 2 {
			name := strings.SplitN(a[2:], "=", 2)[0]
			b.WriteString("norm")
			flaggapQuote(&b, name, string(nf(f, name)))
			b.WriteString("\n")
		}
	}
	b.WriteString("interspersed " + strconv.FormatBool(f.interspersed) + "\n")
	b.WriteString("args")
	flaggapQuote(&b, arguments...)
	b.WriteString("\n")
	f.errorHandling = ContinueOnError
	f.SetOutput(io.Discard)
	f.Usage = func() {}
	if err := f.flaggapParse(arguments); err != nil {
		b.WriteString("err")
		flaggapQuote(&b, err.Error())
		b.WriteString("\n")
	}
	b.WriteString("rest")
	flaggapQuote(&b, f.Args()...)
	b.WriteString("\nset")
	f.Visit(func(fl *Flag) { flaggapQuote(&b, fl.Name) })
	b.WriteString("\n")
	flaggapWrite(path, &b)
}
`
