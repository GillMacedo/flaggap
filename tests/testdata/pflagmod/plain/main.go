// Command plain defines pflag flags of many kinds, with shorthands, and
// prints what it parsed as JSON. Tests compare flaggap with its real parse.
package main

import (
	"encoding/json"
	"os"
	"time"

	"github.com/spf13/pflag"
)

func main() {
	out := map[string]any{}
	b := pflag.BoolP("debug", "d", false, "")
	i := pflag.IntP("workers", "w", 4, "")
	i8 := pflag.Int8("nice", 0, "")
	u16 := pflag.Uint16P("port", "p", 80, "")
	f32 := pflag.Float32("ratio", 0.5, "")
	d := pflag.DurationP("timeout", "t", time.Second, "")
	s := pflag.StringP("name", "n", "x", "")
	c := pflag.CountP("verbose", "v", "")
	ss := pflag.StringSlice("tags", nil, "")
	ip := pflag.IP("bind", nil, "")
	cidr := pflag.IPNet("allow", pflagNet(), "")
	m := pflag.StringToString("labels", nil, "")
	old := pflag.Int("threads", 0, "")
	pflag.CommandLine.MarkDeprecated("threads", "use --workers")
	pflag.Parse()
	out["debug"], out["workers"], out["nice"], out["port"], out["ratio"] = *b, *i, *i8, *u16, *f32
	out["timeout"], out["name"], out["verbose"], out["tags"], out["bind"] = d.String(), *s, *c, *ss, ip.String()
	out["allow"], out["labels"], out["threads"], out["args"] = cidr.String(), *m, *old, pflag.Args()
	json.NewEncoder(os.Stdout).Encode(out)
}
