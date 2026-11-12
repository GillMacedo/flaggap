package tests

import (
	"archive/zip"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A program whose module imports a parser flaggap doesn't model is refused
// with that reason, before it is built or run. The kingpin module here is a
// local stand-in with the real import path.
func TestOtherParserRefused(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "go.mod", "module example.com/kp\n\ngo 1.22\n\nrequire github.com/alecthomas/kingpin/v2 v2.4.0\n\nreplace github.com/alecthomas/kingpin/v2 => ./kingpin\n")
	writeFile(t, dir, "main.go", "package main\n\nimport (\n\t\"flag\"\n\n\t\"github.com/alecthomas/kingpin/v2\"\n)\n\nfunc main() { kingpin.Parse(); flag.Parse() }\n")
	os.Mkdir(filepath.Join(dir, "kingpin"), 0o755)
	writeFile(t, dir, "kingpin/go.mod", "module github.com/alecthomas/kingpin/v2\n\ngo 1.22\n")
	writeFile(t, dir, "kingpin/kingpin.go", "package kingpin\n\n// Parse is a stand-in.\nfunc Parse() {}\n")
	r := runTool(t, nil, "--pkg", dir, "--", "--source=service")
	if r.code != 2 || r.stdout != "" || !strings.Contains(r.stderr, "imports github.com/alecthomas/kingpin/v2, a command-line parser flaggap doesn't read") {
		t.Errorf("exit %d\n%s%s", r.code, r.stdout, r.stderr)
	}
}

// pflag from the module cache can't be patched with go build -overlay, so
// flaggap builds against a patched copy with a replace in a copy of go.mod,
// or of go.work in workspace mode. These tests fill an empty module cache
// from a file-based GOPROXY made from the vendored pflag and cobra, so they
// run offline, and check that the user's go.mod and go.work don't change.
func TestModuleCacheAndWorkspace(t *testing.T) {
	if testing.Short() {
		t.Skip("builds cobra from a fresh module cache")
	}
	proxy := t.TempDir()
	for _, m := range []struct{ path, version, dir string }{
		{"github.com/spf13/pflag", "v1.1.0", "../examples/worker/vendor/github.com/spf13/pflag"},
		{"github.com/spf13/cobra", "v1.10.2", "../examples/worker/vendor/github.com/spf13/cobra"},
		{"github.com/inconshreveable/mousetrap", "v1.1.0", "../examples/worker/vendor/github.com/inconshreveable/mousetrap"},
	} {
		gomod := "module " + m.path + "\n"
		switch m.path {
		case "github.com/spf13/cobra":
			gomod += "\ngo 1.15\n\nrequire (\n\tgithub.com/inconshreveable/mousetrap v1.1.0\n\tgithub.com/spf13/pflag v1.1.0\n)\n"
		}
		makeProxyModule(t, proxy, m.path, m.version, m.dir, gomod)
	}
	work := t.TempDir()
	mod := filepath.Join(work, "worker")
	copyDir(t, "../examples/worker", mod, "vendor")
	os.Remove(filepath.Join(mod, "go.sum")) // it has the real modules' sums
	cache := t.TempDir()
	t.Cleanup(func() {
		c := exec.Command("go", "clean", "-modcache")
		c.Env = append(os.Environ(), "GOMODCACHE="+cache)
		c.Run()
	})
	env := append(os.Environ(), "GOMODCACHE="+cache, "GOPROXY=file://"+filepath.ToSlash(proxy), "GOFLAGS=-mod=mod", "GOSUMDB=off", "GOWORK=off")
	tidy := exec.Command("go", "mod", "download")
	tidy.Dir, tidy.Env = mod, env
	if out, err := tidy.CombinedOutput(); err != nil {
		t.Fatalf("go mod download: %v\n%s", err, out)
	}
	before, _ := os.ReadFile(filepath.Join(mod, "go.mod"))
	check := func(label string, env []string) {
		t.Helper()
		cmd := exec.Command(bin, "--format", "json", "--fail-on", "never", "--pkg", mod, filepath.Join(mod, "broken.yaml"))
		cmd.Env = env
		var stderr strings.Builder
		cmd.Stderr = &stderr
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("%s: %v\n%s%s", label, err, out, stderr.String())
		}
		if !strings.Contains(string(out), `"code": "single-dash"`) || !strings.Contains(string(out), `"parser": "pflag"`) {
			t.Errorf("%s: unexpected report:\n%s", label, out)
		}
		after, _ := os.ReadFile(filepath.Join(mod, "go.mod"))
		if string(after) != string(before) {
			t.Errorf("%s: flaggap changed go.mod", label)
		}
	}
	check("module cache", env)
	writeFile(t, work, "go.work", "go 1.22\n\nuse ./worker\n")
	var wenv []string
	for _, e := range env {
		if e != "GOFLAGS=-mod=mod" && e != "GOWORK=off" {
			wenv = append(wenv, e)
		}
	}
	wenv = append(wenv, "GOFLAGS=", "GOWORK="+filepath.Join(work, "go.work"))
	check("workspace", wenv)
	if data, _ := os.ReadFile(filepath.Join(work, "go.work")); string(data) != "go 1.22\n\nuse ./worker\n" {
		t.Errorf("flaggap changed go.work:\n%s", data)
	}
}

func makeProxyModule(t *testing.T, proxy, path, version, dir, gomod string) {
	t.Helper()
	base := filepath.Join(proxy, path, "@v")
	if err := os.MkdirAll(base, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, base, "list", version+"\n")
	writeFile(t, base, version+".info", `{"Version":"`+version+`","Time":"2025-01-01T00:00:00Z"}`)
	writeFile(t, base, version+".mod", gomod)
	f, err := os.Create(filepath.Join(base, version+".zip"))
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	w, _ := zw.Create(path + "@" + version + "/go.mod")
	w.Write([]byte(gomod))
	filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || filepath.Base(p) == "go.mod" {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		w, _ := zw.Create(path + "@" + version + "/" + filepath.ToSlash(rel))
		w.Write(data)
		return nil
	})
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	f.Close()
}

func copyDir(t *testing.T, src, dst, skip string) {
	t.Helper()
	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		if rel == skip {
			return filepath.SkipDir
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
}
