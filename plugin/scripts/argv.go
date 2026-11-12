package main

import (
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// argTok is one command-line argument after Kubernetes $(VAR) expansion.
type argTok struct {
	text    string
	raw     string // as written in the manifest, before $(VAR) expansion
	unknown bool   // part of it comes from a variable whose value isn't in the input
	vars    string // the variables it couldn't resolve, for messages
}

// where says which container, or the command line, the arguments belong to.
type where struct {
	Kind, Namespace, Name, Container, File string
}

func (w where) String() string {
	if w.Kind == "" {
		return "command line"
	}
	ns := ""
	if w.Namespace != "" {
		ns = w.Namespace + "/"
	}
	return fmt.Sprintf("%s %s%s, container %s", w.Kind, ns, w.Name, w.Container)
}

// checker replays the program's flag parser over the arguments with the
// flag table the program reported. It stops at the first argument that
// makes the parse fail, and records which arguments those were and what
// the real parser should say, so the caller can compare.
type checker struct {
	prog     *program
	at       where
	args     []argTok
	findings []*finding
	real     []string // the arguments to give the real parser: unknown values replaced or dropped
	fatal    *finding // the finding that makes the parse fail
	fatalAt  [2]int   // its arguments, args[fatalAt[0]:fatalAt[1]]
	goMsg    string   // text the real parser's error must contain
	stopped  bool
	mark     int // len(real) when the current argument started
}

func (c *checker) add(sev int, code, arg, msg, fix string) *finding {
	f := &finding{
		sev: sev, Severity: sevNames[sev], Code: code, Kind: c.at.Kind, Namespace: c.at.Namespace,
		Name: c.at.Name, Container: c.at.Container, Arg: arg, Message: msg, Fix: fix, File: c.at.File,
	}
	c.findings = append(c.findings, f)
	return f
}

// fail records the finding that stops the parse.
func (c *checker) fail(from, to int, goMsg, code, arg, msg, fix string) {
	c.fatal = c.add(sevError, code, arg, msg, fix)
	c.fatalAt = [2]int{from, to}
	c.goMsg = goMsg
	c.stopped = true
	// The real parser gets the failing arguments as written.
	c.real = append(c.real[:c.mark], texts(c.args[from:])...)
}

// dashName writes a flag the way the program's parser prints it.
func (c *checker) dashName(name string) string {
	if c.prog.Mode == "flag" {
		return "-" + name
	}
	return "--" + name
}

func (c *checker) check(args []argTok) {
	c.args = args
	if c.prog.Mode == "flag" {
		c.checkStd()
	} else {
		c.checkPflag()
	}
}

func (c *checker) lookup(name string) *flagDef {
	if n, ok := c.prog.norm[name]; ok {
		name = n
	}
	return c.prog.Flags[name]
}

// unresolvedName stops the check at an argument whose flag name, or whether
// it is a flag at all, depends on a variable flaggap can't see.
func (c *checker) unresolvedName(t argTok) {
	c.add(sevNote, "not-checked", t.text, fmt.Sprintf("%s comes from %s, so flaggap can't tell what it is, and the arguments from here on weren't checked", t.text, t.vars), "")
	c.stopped = true
}

func (c *checker) checkStd() {
	seen := map[*flagDef]string{}
	args := c.args
	for i := 0; i < len(args) && !c.stopped; i++ {
		c.mark = len(c.real)
		t := args[i]
		s := t.text
		if t.unknown && !strings.HasPrefix(s, "-") {
			c.unresolvedName(t)
			return
		}
		if len(s) < 2 || s[0] != '-' {
			c.real = append(c.real, s)
			c.positional(i)
			return
		}
		minus := 1
		if s[1] == '-' {
			minus++
			if len(s) == 2 {
				c.real = append(c.real, texts(args[i:])...)
				return
			}
		}
		name := s[minus:]
		if len(name) == 0 || name[0] == '-' || name[0] == '=' {
			c.fail(i, i+1, "bad flag syntax: "+s, "bad-syntax", s, fmt.Sprintf("the flag package rejects %s, so the program exits at startup with \"bad flag syntax: %s\"", s, s), "")
			return
		}
		value, hasValue := "", false
		if k := strings.Index(name, "="); k >= 0 {
			name, value, hasValue = name[:k], name[k+1:], true
		}
		if strings.Contains(name, "$(") {
			c.unresolvedName(t)
			return
		}
		d := c.lookup(name)
		if d == nil {
			if name == "help" || name == "h" {
				c.fail(i, i+1, "help requested", "exits", s, fmt.Sprintf("%s makes the flag package print the usage and the program exit, so the container stops right after it starts", s), "remove it")
				return
			}
			c.unknown(i, hasValue, s, name, "flag provided but not defined: -"+name)
			return
		}
		start := i
		if d.boolLike {
			if hasValue {
				c.value(start, i, d, s, value, t, true)
			} else {
				c.real = append(c.real, s)
				if i+1 < len(args) && takesNoValue(d, args[i+1].text) {
					c.boolSeparate(s, d, args[i+1].text)
				}
			}
		} else if hasValue {
			c.value(start, i, d, s, value, t, true)
		} else {
			if i+1 >= len(args) {
				c.fail(i, i+1, "flag needs an argument: -"+name, "missing-value", s, fmt.Sprintf("%s is the last argument and needs a value, so the program exits at startup with \"flag needs an argument: -%s\"", s, name), "")
				return
			}
			i++
			c.valueIsFlag(s, args[i].text)
			c.real = append(c.real, s)
			c.value(start, i, d, s, args[i].text, args[i], false)
		}
		c.repeat(seen, d, s)
	}
}

func (c *checker) checkPflag() {
	seen := map[*flagDef]string{}
	args := c.args
	for i := 0; i < len(args) && !c.stopped; i++ {
		c.mark = len(c.real)
		t := args[i]
		s := t.text
		if t.unknown && !strings.HasPrefix(s, "-") {
			c.unresolvedName(t)
			return
		}
		if len(s) < 2 || s[0] != '-' {
			c.real = append(c.real, s)
			if c.prog.interOff {
				c.positional(i)
				return
			}
			continue // pflag reads flags after positional arguments too
		}
		if s[1] == '-' {
			if len(s) == 2 {
				c.real = append(c.real, texts(args[i:])...)
				return
			}
			name := s[2:]
			if name[0] == '-' || name[0] == '=' {
				c.fail(i, i+1, "bad flag syntax: "+s, "bad-syntax", s, fmt.Sprintf("pflag rejects %s, so the program exits at startup with \"bad flag syntax: %s\"", s, s), "")
				return
			}
			value, hasValue := "", false
			if k := strings.Index(name, "="); k >= 0 {
				name, value, hasValue = name[:k], name[k+1:], true
			}
			if strings.Contains(name, "$(") {
				c.unresolvedName(t)
				return
			}
			d := c.lookup(name)
			if d == nil {
				if name == "help" {
					c.fail(i, i+1, "help requested", "exits", s, fmt.Sprintf("%s makes pflag print the usage and the program exit, so the container stops right after it starts", s), "remove it")
					return
				}
				c.unknown(i, hasValue, s, name, "unknown flag: --"+name)
				return
			}
			if d.Name == "help" {
				c.real = append(c.real, s)
				c.add(sevError, "exits", s, fmt.Sprintf("%s makes the program print its help and exit, so the container stops right after it starts", s), "remove it")
				c.stopped = true
				return
			}
			start := i
			switch {
			case hasValue:
				c.value(start, i, d, s, value, t, true)
			case d.boolLike:
				c.real = append(c.real, s)
				if i+1 < len(args) && takesNoValue(d, args[i+1].text) {
					c.boolSeparate(s, d, args[i+1].text)
				}
			default:
				if i+1 >= len(args) {
					c.fail(i, i+1, "flag needs an argument: "+s, "missing-value", s, fmt.Sprintf("%s is the last argument and needs a value, so the program exits at startup with \"flag needs an argument: %s\"", s, s), "")
					return
				}
				i++
				c.valueIsFlag(s, args[i].text)
				c.real = append(c.real, s)
				c.value(start, i, d, s, args[i].text, args[i], false)
			}
			c.repeat(seen, d, s)
			continue
		}
		// One dash: pflag reads a group of one-letter shorthands.
		shorts := s[1:]
		long, value, hasValue := strings.Cut(shorts, "=")
		if strings.Contains(shorts, "$(") && strings.HasPrefix(shorts, "$(") {
			c.unresolvedName(t)
			return
		}
		fix := ""
		if def := c.lookup(long); len(long) > 1 && def != nil {
			fix = "write --" + def.Name
			if hasValue {
				fix += "=" + value
			}
			if sd := c.prog.Shorthands[long[:1]]; sd != nil && !sd.boolLike {
				v := shorts[1:]
				if err := valueErr(sd, v); err != nil {
					c.fail(i, i+1, c.valueGoMsg(sd, v), "single-dash", s, fmt.Sprintf("pflag reads one dash as one-letter shorthands, so %s isn't --%s: it gives -%s (--%s) the value %q, which isn't a valid %s (%v), and the program exits at startup", s, def.Name, long[:1], sd.Name, v, kindWord(sd.Kind), err), fix)
					return
				}
				msg := fmt.Sprintf("pflag reads one dash as one-letter shorthands, so %s isn't --%s: it sets -%s (--%s) to %q, and --%s keeps its default", s, def.Name, long[:1], sd.Name, v, def.Name)
				if sd == def {
					msg = fmt.Sprintf("pflag reads one dash as one-letter shorthands, so %s is the shorthand -%s with the value %q: --%s gets %q", s, long[:1], v, def.Name, v)
				}
				c.add(sevError, "single-dash", s, msg, fix)
				c.real = append(c.real, s)
				c.repeat(seen, sd, s)
				continue
			}
		}
		start := i
		consumed := false
		for len(shorts) > 0 && !c.stopped {
			ch := shorts[:1]
			d := c.prog.Shorthands[ch]
			if d == nil {
				if ch == "h" {
					c.fail(i, i+1, "help requested", "exits", s, fmt.Sprintf("%s makes pflag print the usage and the program exit, so the container stops right after it starts", s), "remove it")
					return
				}
				goMsg := fmt.Sprintf("unknown shorthand flag: %q in -%s", rune(ch[0]), shorts)
				if fix != "" {
					c.fail(i, i+1, goMsg, "single-dash", s, fmt.Sprintf("pflag reads one dash as one-letter shorthands, so the program exits at startup with %q", goMsg), fix)
				} else {
					c.fail(i, i+1, goMsg, "unknown-flag", s, fmt.Sprintf("-%s is not a shorthand flag of %s, so the program exits at startup with %q", ch, c.prog.Path, goMsg), "remove it")
				}
				return
			}
			if d.Name == "help" {
				c.real = append(c.real, s)
				c.add(sevError, "exits", s, fmt.Sprintf("%s makes the program print its help and exit, so the container stops right after it starts", s), "remove it")
				c.stopped = true
				return
			}
			switch {
			case len(shorts) > 2 && shorts[1] == '=':
				c.checkValue(start, i, d, s, shorts[2:], t)
				shorts = ""
			case d.boolLike:
				shorts = shorts[1:]
				if shorts == "" && i+1 < len(args) && takesNoValue(d, args[i+1].text) {
					c.boolSeparate(s, d, args[i+1].text)
				}
			case len(shorts) > 1:
				c.checkValue(start, i, d, s, shorts[1:], t)
				shorts = ""
			default:
				if i+1 >= len(args) {
					c.fail(i, i+1, fmt.Sprintf("flag needs an argument: '%s' in %s", ch, s), "missing-value", s, fmt.Sprintf("%s is the last argument and needs a value, so the program exits at startup with \"flag needs an argument: '%s' in %s\"", s, ch, s), "")
					return
				}
				i++
				consumed = true
				c.checkValue(start, i, d, s, args[i].text, args[i])
				shorts = ""
			}
			c.repeat(seen, d, s)
		}
		if c.stopped {
			return
		}
		if t.unknown || (consumed && args[i].unknown) {
			// Shorthand groups with variables aren't rewritten; drop them.
			c.add(sevNote, "not-checked", s, fmt.Sprintf("the value of %s comes from %s, so it wasn't checked", s, t.vars+args[i].vars), "")
			continue
		}
		c.real = append(c.real, texts(args[start:i+1])...)
	}
}

func texts(ts []argTok) []string {
	var out []string
	for _, t := range ts {
		out = append(out, t.text)
	}
	return out
}

func isBoolWord(s string) bool {
	_, err := strconv.ParseBool(s)
	return err == nil
}

// takesNoValue reports whether next looks like a value meant for a flag
// that never takes a separate argument: a boolean word, or a number after
// a count flag.
func takesNoValue(d *flagDef, next string) bool {
	if d.Kind == "count" {
		_, err := strconv.Atoi(next)
		return err == nil
	}
	return isBoolWord(next)
}

func (c *checker) unknown(i int, hasValue bool, s, name, goMsg string) {
	msg := fmt.Sprintf("%s is not a flag of %s, so the program exits at startup with \"%s\"", s, c.prog.Path, goMsg)
	fix := "remove it"
	if near := nearest(name, c.prog.names()); near != "" {
		fix += ", or did you mean " + c.dashName(near) + "?"
	}
	end := i + 1
	if !hasValue && i+1 < len(c.args) && !strings.HasPrefix(c.args[i+1].text, "-") {
		// Probably its value; drop it too when looking for the next problem.
		end = i + 2
	}
	c.fail(i, end, goMsg, "unknown-flag", s, msg, fix)
}

func (c *checker) valueIsFlag(s, next string) {
	if !strings.HasPrefix(next, "-") || len(next) < 2 {
		return
	}
	name := strings.SplitN(strings.TrimLeft(next, "-"), "=", 2)[0]
	if c.lookup(name) == nil && c.prog.Shorthands[name] == nil {
		return
	}
	c.add(sevError, "value-is-a-flag", s+" "+next, fmt.Sprintf("%s takes a value, so the parser takes the next argument, %s, as that value, and %s is never set", s, next, next), fmt.Sprintf("give %s its value, as %s=VALUE", s, s))
}

func (c *checker) boolSeparate(s string, d *flagDef, word string) {
	msg := fmt.Sprintf("%s is a boolean flag, which never takes the next argument as its value: %s sets it to true and %q is read as a positional argument", s, s, word)
	fix := fmt.Sprintf("write %s=%s", s, word)
	if d.Kind == "count" {
		msg = fmt.Sprintf("%s is a count flag, which never takes the next argument as its value: %s adds one and %q is read as a positional argument", s, s, word)
		if strings.HasPrefix(s, "--") {
			fix = fmt.Sprintf("write %s=%s", s, word)
		} else {
			fix = fmt.Sprintf("write --%s=%s", d.Name, word)
		}
	}
	if c.prog.Mode == "flag" || c.prog.interOff {
		msg += ", where the parser stops reading flags"
	}
	c.add(sevError, "bool-value-ignored", s+" "+word, msg, fix)
}

// positional handles the first positional argument when the parser stops
// there (the flag package, or pflag with interspersed flags off).
func (c *checker) positional(i int) {
	var flags []string
	for _, r := range c.args[i+1:] {
		if r.text == "--" {
			break
		}
		if len(r.text) > 1 && r.text[0] == '-' {
			flags = append(flags, r.text)
		}
	}
	c.real = append(c.real, texts(c.args[i+1:])...)
	if len(flags) == 0 {
		return
	}
	list := strings.Join(flags, " ")
	pos := c.args[i].text
	if c.prog.Subcommands {
		c.add(sevWarn, "after-positional", list, fmt.Sprintf("the program's flag set stops at %q, so %s %s left for the program's own code, which creates its own flag sets; flaggap didn't check them", pos, list, plural(len(flags), "is", "are")), "")
		return
	}
	c.add(sevError, "ignored-after-positional", list, fmt.Sprintf("the parser stops at the first argument that isn't a flag, %q, so %s %s never parsed and the program runs with %s default%s", pos, list, plural(len(flags), "is", "are"), plural(len(flags), "its", "their"), plural(len(flags), "", "s")), "move the flags before "+strconv.Quote(pos)+", or remove it")
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

func (c *checker) repeat(seen map[*flagDef]string, d *flagDef, s string) {
	prev, again := seen[d]
	if d.Deprecated != "" && !again {
		c.add(sevWarn, "deprecated", s, fmt.Sprintf("--%s is deprecated: %s", d.Name, d.Deprecated), "")
	}
	if again && d.scalar {
		c.add(sevWarn, "repeated", s, fmt.Sprintf("%s is set twice (%s, then %s); the last one wins", c.dashName(d.Name), prev, s), "keep one")
	}
	if !again {
		seen[d] = s
	}
}

// placeholder is a value the flag accepts, given to the real parser in place
// of a value flaggap can't see.
func placeholder(kind string) (string, bool) {
	switch kind {
	case "bool":
		return "true", true
	case "int", "uint", "float", "count":
		return "0", true
	case "duration":
		return "0s", true
	case "ip":
		return "127.0.0.1", true
	case "ipnet":
		return "10.0.0.0/8", true
	case "string":
		return "x", true
	}
	return "", false
}

// value checks a flag's value, and adds the flag to the real arguments:
// args[start:end+1] are the flag and, when separate, its value.
func (c *checker) value(start, end int, d *flagDef, arg, v string, t argTok, inline bool) {
	if t.unknown {
		ph, ok := placeholder(d.Kind)
		if d.Kind != "string" {
			c.add(sevNote, "not-checked", arg, fmt.Sprintf("the value of %s comes from %s, so it wasn't checked", c.dashName(d.Name), t.vars), "")
		}
		if !ok {
			return // dropped from the real arguments
		}
		if inline {
			c.real = append(c.real, arg[:strings.Index(arg, "=")+1]+ph)
		} else {
			c.real = append(c.real, ph)
		}
		return
	}
	if inline {
		c.real = append(c.real, arg)
	} else {
		c.real = append(c.real, v)
	}
	c.checkValue(start, end, d, arg, v, t)
}

// valueErr parses a value the way the flag's Set method does. Kinds it
// can't judge return nil; the real parser judges them.
func valueErr(d *flagDef, v string) error {
	var err error
	switch d.Kind {
	case "bool":
		_, err = strconv.ParseBool(v)
	case "int":
		_, err = strconv.ParseInt(v, 0, d.Bits)
	case "uint":
		_, err = strconv.ParseUint(v, 0, d.Bits)
	case "float":
		_, err = strconv.ParseFloat(v, d.Bits)
	case "duration":
		_, err = time.ParseDuration(v)
	case "count":
		if v != "+1" {
			_, err = strconv.ParseInt(v, 0, 0)
		}
	case "ip":
		if net.ParseIP(strings.TrimSpace(v)) == nil {
			err = fmt.Errorf("not an IP address")
		}
	case "ipnet":
		_, _, err = net.ParseCIDR(strings.TrimSpace(v))
	}
	if ne, ok := err.(*strconv.NumError); ok {
		err = ne.Err
	}
	return err
}

func (c *checker) valueGoMsg(d *flagDef, v string) string {
	switch {
	case c.prog.Mode == "pflag":
		return fmt.Sprintf("invalid argument %q for", v)
	case d.Kind == "bool":
		return fmt.Sprintf("invalid boolean value %q for -%s", v, d.Name)
	}
	return fmt.Sprintf("invalid value %q for flag -%s", v, d.Name)
}

// checkValue fails the parse when the value doesn't parse.
func (c *checker) checkValue(start, end int, d *flagDef, arg, v string, t argTok) {
	if t.unknown {
		return
	}
	err := valueErr(d, v)
	if err == nil {
		return
	}
	fix := ""
	switch d.Kind {
	case "duration":
		if _, e := strconv.ParseFloat(v, 64); e == nil {
			fix = fmt.Sprintf("add a unit, such as %ss", v)
		}
	case "bool":
		fix = "use true or false"
	}
	msg := fmt.Sprintf("%q is not a valid %s for %s (%v), so the program exits at startup", v, kindWord(d.Kind), c.dashName(d.Name), err)
	if t.raw != "" && t.raw != t.text {
		msg += fmt.Sprintf(". The manifest writes it as %s", t.raw)
	}
	c.fail(start, end+1, c.valueGoMsg(d, v), "bad-value", arg, msg, fix)
}

func kindWord(k string) string {
	switch k {
	case "bool":
		return "boolean"
	case "int", "uint":
		return "whole number"
	case "float":
		return "number"
	case "duration":
		return "duration"
	case "count":
		return "count"
	case "ip":
		return "IP address"
	case "ipnet":
		return "CIDR"
	}
	return k
}

// nearest finds a defined name a typo probably meant.
func nearest(name string, names []string) string {
	norm := func(s string) string { return strings.ToLower(strings.NewReplacer("_", "-", ".", "-").Replace(s)) }
	best, bestD := "", 1<<30
	for _, n := range names {
		if norm(n) == norm(name) {
			return n
		}
		d := levenshtein(name, n)
		if d < bestD || (d == bestD && n < best) {
			best, bestD = n, d
		}
	}
	limit := 2
	if len(name) >= 12 {
		limit = 3
	}
	if bestD <= limit && bestD < len(name)/2+1 {
		return best
	}
	return byWords(name, names)
}

// byWords suggests the defined name that shares the most words with name,
// counting a word that starts another (addr, address) as shared, when at
// least two words match and no other name matches as many.
func byWords(name string, names []string) string {
	split := func(s string) []string {
		return strings.FieldsFunc(strings.ToLower(s), func(r rune) bool { return r == '-' || r == '_' || r == '.' })
	}
	match := func(a, b string) bool {
		if len(a) > len(b) {
			a, b = b, a
		}
		return len(a) >= 4 && strings.HasPrefix(b, a) || a == b
	}
	words := split(name)
	best, bestN, tie := "", 0, false
	for _, n := range names {
		count := 0
		for _, w := range words {
			for _, v := range split(n) {
				if match(w, v) {
					count++
					break
				}
			}
		}
		switch {
		case count > bestN:
			best, bestN, tie = n, count, false
		case count == bestN:
			tie = true
		}
	}
	if bestN >= 2 && !tie {
		return best
	}
	return ""
}

func levenshtein(a, b string) int {
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(b)]
}

var (
	stdValueErr   = regexp.MustCompile(`^invalid (?:boolean )?value "(?:[^"\\]|\\.)*" for (?:flag )?-([^:\s]+): `)
	pflagValueErr = regexp.MustCompile(`^invalid argument "(?:[^"\\]|\\.)*" for "(?:-(\S), )?--([^"]+)" flag: `)
)

// argsOf finds the arguments that set the flag a real value error names:
// the last ones, because a later value replaces an earlier one only after
// the earlier one parsed.
func (c *checker) argsOf(errText string) (int, int, bool) {
	var name, short string
	if m := stdValueErr.FindStringSubmatch(errText); m != nil {
		name = m[1]
	} else if m := pflagValueErr.FindStringSubmatch(errText); m != nil {
		short, name = m[1], m[2]
	} else {
		return 0, 0, false
	}
	d := c.prog.Flags[name]
	if d == nil {
		return 0, 0, false
	}
	for i, t := range c.args {
		s := t.text
		if s == "--" || (c.prog.Mode == "flag" && !strings.HasPrefix(s, "-")) {
			break
		}
		long := strings.SplitN(strings.TrimLeft(s, "-"), "=", 2)[0]
		isLong := strings.HasPrefix(s, "--") || c.prog.Mode == "flag"
		hit := isLong && c.lookup(long) == d
		if !isLong && short != "" && strings.HasPrefix(s, "-"+short) {
			hit = true
		}
		if !hit {
			continue
		}
		if strings.Contains(s, "=") || (!isLong && len(s) > 2) || d.boolLike {
			return i, i + 1, true
		}
		if i+1 < len(c.args) {
			return i, i + 2, true
		}
		return i, i + 1, true
	}
	return 0, 0, false
}
