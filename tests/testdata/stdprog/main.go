// Command stdprog defines one flag of every kind the flag package has, and
// prints what it parsed as JSON. Tests compare flaggap with its real parse.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"net"
	"os"
	"strings"
	"time"
)

// level is a custom flag.Value that accepts only some words.
type level string

func (l *level) String() string { return string(*l) }
func (l *level) Set(s string) error {
	switch s {
	case "debug", "info", "warn", "error":
		*l = level(s)
		return nil
	}
	return errors.New("must be debug, info, warn or error")
}

// toggle is a custom boolean flag: -trace with no value sets it.
type toggle bool

func (t *toggle) String() string   { return "false" }
func (t *toggle) IsBoolFlag() bool { return true }
func (t *toggle) Set(s string) error {
	*t = s == "true"
	if s != "true" && s != "false" {
		return errors.New("true or false")
	}
	return nil
}

func main() {
	out := map[string]any{}
	b := flag.Bool("debug", false, "")
	i := flag.Int("workers", 4, "")
	i64 := flag.Int64("max-bytes", 0, "")
	u := flag.Uint("retries", 3, "")
	u64 := flag.Uint64("seed", 0, "")
	f := flag.Float64("ratio", 0.5, "")
	d := flag.Duration("timeout", time.Second, "")
	s := flag.String("listen", ":8080", "")
	var lv level = "info"
	flag.Var(&lv, "log-level", "")
	var tr toggle
	flag.Var(&tr, "trace", "")
	var tags []string
	flag.Func("tag", "", func(v string) error { tags = append(tags, v); return nil })
	pretty := false
	flag.BoolFunc("pretty", "", func(string) error { pretty = true; return nil })
	ip := net.IPv4(127, 0, 0, 1)
	flag.TextVar(&ip, "bind-ip", ip, "")
	flag.Parse()
	out["debug"], out["workers"], out["max-bytes"], out["retries"], out["seed"] = *b, *i, *i64, *u, *u64
	out["ratio"], out["timeout"], out["listen"], out["log-level"], out["trace"] = *f, d.String(), *s, string(lv), bool(tr)
	out["tag"], out["pretty"], out["bind-ip"], out["args"] = strings.Join(tags, ","), pretty, ip.String(), flag.Args()
	json.NewEncoder(os.Stdout).Encode(out)
}
