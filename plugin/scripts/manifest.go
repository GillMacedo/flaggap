package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Loading and cut-off detection follow golimits by the same author.

type object struct {
	File      string
	Kind      string
	Namespace string
	Name      string
	raw       map[string]any
	endPath   []string // non-nil on a file's last object unless the file ends with --- or ...
}

func load(paths []string, stdin io.Reader) ([]*object, error) {
	var objs []*object
	for _, p := range paths {
		if p == "-" {
			data, err := io.ReadAll(stdin)
			if err != nil {
				return nil, untrusted("reading stdin: %v", err)
			}
			o, err := parseFile("stdin", data)
			if err != nil {
				return nil, err
			}
			objs = append(objs, o...)
			continue
		}
		st, err := os.Stat(p)
		if err != nil {
			return nil, untrusted("%v", err)
		}
		if !st.IsDir() {
			data, err := os.ReadFile(p)
			if err != nil {
				return nil, untrusted("%v", err)
			}
			o, err := parseFile(p, data)
			if err != nil {
				return nil, err
			}
			objs = append(objs, o...)
			continue
		}
		var files []string
		err = filepath.WalkDir(p, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			name := d.Name()
			if d.Type()&fs.ModeSymlink != 0 {
				return untrusted("%s is a symbolic link, which flaggap doesn't follow; pass its target instead", path)
			}
			if d.IsDir() {
				if path != p && strings.HasPrefix(name, ".") {
					return filepath.SkipDir
				}
				return nil
			}
			switch name {
			case "Chart.yaml":
				return untrusted("%s is a Helm chart; render it first with helm template and pass the output", filepath.Dir(path))
			case "kustomization.yaml", "kustomization.yml", "Kustomization":
				return untrusted("%s is a kustomization; render it first with kustomize build and pass the output", filepath.Dir(path))
			}
			switch strings.ToLower(filepath.Ext(name)) {
			case ".yaml", ".yml", ".json":
				files = append(files, path)
			}
			return nil
		})
		if err != nil {
			var ie *inputError
			if errors.As(err, &ie) {
				return nil, err
			}
			return nil, untrusted("%v", err)
		}
		if len(files) == 0 {
			return nil, untrusted("%s has no .yaml, .yml or .json files", p)
		}
		sort.Strings(files)
		for _, f := range files {
			data, err := os.ReadFile(f)
			if err != nil {
				return nil, untrusted("%v", err)
			}
			o, err := parseFile(f, data)
			if err != nil {
				return nil, err
			}
			objs = append(objs, o...)
		}
	}
	return objs, nil
}

func parseFile(name string, data []byte) ([]*object, error) {
	if len(bytes.TrimSpace(data)) == 0 {
		return nil, untrusted("%s is empty", name)
	}
	if !bytes.HasSuffix(data, []byte("\n")) {
		return nil, untrusted("%s doesn't end with a line break, so it may be cut off mid-line; helm template and kubectl always end their output with one", name)
	}
	if bytes.Contains(data, []byte("\x00")) {
		return nil, untrusted("%s contains NUL bytes; it isn't a text manifest", name)
	}
	if bytes.Contains(data, []byte("{{")) {
		return nil, untrusted("%s contains {{ template markers; render it first with helm template", name)
	}
	if last := lastLine(data); strings.HasSuffix(last, ":") || last == "-" {
		return nil, untrusted("%s ends with %q, a key with no value, so it looks cut off", name, last)
	}
	// Each document is decoded twice: into plain maps for the values, which
	// keeps YAML merge keys the way kubectl does, and into ordered maps only
	// to find where the document ends.
	dec := NewDecoder(bytes.NewReader(data))
	ord := NewDecoder(bytes.NewReader(data))
	var out []*object
	var lastObjs []*object
	var lastPath []string
	var lastList bool
	for n := 1; ; n++ {
		var doc any
		err := dec.Decode(&doc)
		var odoc MapSlice
		oerr := ord.Decode(&odoc)
		if oerr != nil && strings.Contains(oerr.Error(), "cannot unmarshal") {
			oerr = nil // not a mapping; the plain decode reports that
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, untrusted("%s, document %d: not valid YAML or JSON (%v); is it the output of a failed command?", name, n, err)
		}
		if oerr != nil {
			return nil, untrusted("%s, document %d: not valid YAML or JSON (%v)", name, n, oerr)
		}
		if doc == nil {
			continue
		}
		m, ok := toStringMap(doc).(map[string]any)
		if !ok {
			return nil, untrusted("%s, document %d is not a Kubernetes object; is it the output of a failed command?", name, n)
		}
		objs, err := toObjects(name, n, m)
		if err != nil {
			return nil, err
		}
		out = append(out, objs...)
		_, lastList = m["items"].([]any)
		lastObjs, lastPath = objs, endPath(odoc)
	}
	if len(out) == 0 {
		return nil, untrusted("%s has no Kubernetes objects", name)
	}
	if !explicitEnd(data) && len(lastObjs) > 0 {
		last := lastObjs[len(lastObjs)-1]
		switch {
		case lastList && len(lastPath) >= 2 && lastPath[0] == "items":
			// the List ends inside its last item
			if idx, err := strconv.Atoi(lastPath[1]); err == nil && idx == len(lastObjs)-1 {
				last.endPath = lastPath[2:]
			}
		case !lastList:
			last.endPath = lastPath
		}
		if last.endPath == nil {
			last.endPath = []string{}
		}
	}
	return out, nil
}

func toStringMap(v any) any {
	switch t := v.(type) {
	case map[any]any:
		m := make(map[string]any, len(t))
		for k, val := range t {
			m[fmt.Sprint(k)] = toStringMap(val)
		}
		return m
	case []any:
		for i := range t {
			t[i] = toStringMap(t[i])
		}
		return t
	}
	return v
}

// explicitEnd reports whether the file ends with a --- or ... line, which
// says the last document is complete.
func explicitEnd(data []byte) bool {
	lines := strings.Split(string(data), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		t := strings.TrimRight(lines[i], " \t\r")
		if t == "" {
			continue
		}
		return t == "---" || t == "..."
	}
	return false
}

// endPath is the path of the last value in document order.
func endPath(v any) []string {
	var p []string
	for {
		switch t := v.(type) {
		case MapSlice:
			if len(t) == 0 {
				return p
			}
			last := t[len(t)-1]
			p = append(p, fmt.Sprint(last.Key))
			v = last.Value
		case []any:
			if len(t) == 0 {
				return p
			}
			p = append(p, strconv.Itoa(len(t)-1))
			v = t[len(t)-1]
		default:
			return p
		}
	}
}

func lastLine(data []byte) string {
	lines := strings.Split(string(data), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		t := strings.TrimSpace(lines[i])
		if t == "" || strings.HasPrefix(t, "#") || t == "---" {
			continue
		}
		return t
	}
	return ""
}

func toObjects(file string, n int, m map[string]any) ([]*object, error) {
	kind, _ := m["kind"].(string)
	apiVersion, _ := m["apiVersion"].(string)
	if kind == "" || apiVersion == "" {
		return nil, untrusted("%s, document %d has no apiVersion or kind, so it isn't a Kubernetes object; is it the output of a failed command?", file, n)
	}
	if items, ok := m["items"].([]any); ok && strings.HasSuffix(kind, "List") {
		var out []*object
		for i, it := range items {
			im, ok := it.(map[string]any)
			if !ok {
				return nil, untrusted("%s, document %d, item %d is not an object", file, n, i+1)
			}
			o, err := toObjects(file, n, im)
			if err != nil {
				return nil, err
			}
			out = append(out, o...)
		}
		return out, nil
	}
	meta, _ := m["metadata"].(map[string]any)
	name, _ := meta["name"].(string)
	if name == "" {
		name, _ = meta["generateName"].(string)
	}
	if name == "" {
		return nil, untrusted("%s, document %d: %s has no metadata.name, so it looks incomplete", file, n, kind)
	}
	ns, _ := meta["namespace"].(string)
	return []*object{{File: file, Kind: kind, Namespace: ns, Name: name, raw: m}}, nil
}

func path(m map[string]any, keys ...string) any {
	var cur any = m
	for _, k := range keys {
		mm, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur = mm[k]
	}
	return cur
}

var workloadKinds = map[string][]string{
	"Pod":                   {"spec"},
	"Deployment":            {"spec", "template", "spec"},
	"StatefulSet":           {"spec", "template", "spec"},
	"DaemonSet":             {"spec", "template", "spec"},
	"ReplicaSet":            {"spec", "template", "spec"},
	"ReplicationController": {"spec", "template", "spec"},
	"Job":                   {"spec", "template", "spec"},
	"CronJob":               {"spec", "jobTemplate", "spec", "template", "spec"},
	"PodTemplate":           {"template", "spec"},
}

// podSpec finds the pod spec of a workload. Kinds it doesn't know, such as
// an Argo Rollout, count when they have spec.template.spec.containers.
func podSpec(o *object) (map[string]any, bool, error) {
	p, known := workloadKinds[o.Kind]
	if !known {
		p = []string{"spec", "template", "spec"}
	}
	spec, _ := path(o.raw, p...).(map[string]any)
	if !known && o.endPath != nil && path(o.raw, "spec", "template") != nil {
		if _, ok := spec["containers"].([]any); !ok {
			return nil, false, untrusted("%s: %s %s is the file's last object and has spec.template but no containers in it, so it looks cut off. If the file is complete, end it with a line that says ---", o.File, o.Kind, o.Name)
		}
	}
	if spec == nil {
		if known {
			return nil, false, untrusted("%s: %s %s has no pod spec at %s, so it looks cut off", o.File, o.Kind, o.Name, strings.Join(p, "."))
		}
		return nil, false, nil
	}
	if _, ok := spec["containers"].([]any); !ok && !known {
		return nil, false, nil
	}
	if e := o.endPath; len(e) > len(p)+1 && strings.Join(e[:len(p)], ".") == strings.Join(p, ".") &&
		(e[len(p)] == "containers" || e[len(p)] == "initContainers") {
		return nil, false, untrusted("%s ends inside a container of %s %s, which is what a file cut on a line break looks like: arguments after the cut would be missing. If the file is complete, end it with a line that says --- (for example: { helm template ...; echo ---; } | flaggap -)", o.File, o.Kind, o.Name)
	}
	return spec, true, nil
}

// container is one container whose arguments may be checked.
type container struct {
	at      where
	command []any
	args    []any
	hasCmd  bool
	hasArgs bool
	raw     map[string]any
	cms     map[string]map[string]string // ConfigMaps in the input, by namespace/name
}

func containers(objs []*object) ([]*container, int, error) {
	var out []*container
	workloads := 0
	configMaps := map[string]map[string]string{}
	for _, o := range objs {
		if o.Kind == "ConfigMap" {
			data := map[string]string{}
			if d, ok := o.raw["data"].(map[string]any); ok {
				for k, v := range d {
					data[k] = fmt.Sprint(v)
				}
			}
			configMaps[o.Namespace+"/"+o.Name] = data
		}
	}
	for _, o := range objs {
		spec, ok, err := podSpec(o)
		if err != nil {
			return nil, 0, err
		}
		if !ok {
			continue
		}
		workloads++
		for _, field := range []string{"initContainers", "containers"} {
			list, _ := spec[field].([]any)
			if field == "containers" && len(list) == 0 {
				return nil, 0, untrusted("%s: %s %s has no containers, so it looks cut off", o.File, o.Kind, o.Name)
			}
			for i, it := range list {
				m, ok := it.(map[string]any)
				if !ok {
					return nil, 0, untrusted("%s: %s %s: %s[%d] is not an object", o.File, o.Kind, o.Name, field, i)
				}
				name, _ := m["name"].(string)
				image, _ := m["image"].(string)
				if name == "" || image == "" {
					return nil, 0, untrusted("%s: %s %s: %s[%d] has no name or no image, so it looks cut off", o.File, o.Kind, o.Name, field, i)
				}
				c := &container{at: where{Kind: o.Kind, Namespace: o.Namespace, Name: o.Name, Container: name, File: o.File}, raw: m}
				for _, f := range []struct {
					key  string
					list *[]any
					has  *bool
				}{{"command", &c.command, &c.hasCmd}, {"args", &c.args, &c.hasArgs}} {
					v, present := m[f.key]
					if !present || v == nil {
						continue
					}
					l, ok := v.([]any)
					if !ok {
						return nil, 0, untrusted("%s: %s %s, container %s: %s is not a list", o.File, o.Kind, o.Name, name, f.key)
					}
					for _, a := range l {
						if _, ok := a.(string); !ok {
							return nil, 0, untrusted("%s: %s %s, container %s: %s has %v, which isn't a string; the API server rejects it", o.File, o.Kind, o.Name, name, f.key, a)
						}
					}
					*f.list, *f.has = l, true
				}
				c.cms = configMaps
				out = append(out, c)
			}
		}
	}
	if workloads == 0 {
		return nil, 0, untrusted("found %d objects and no workloads (Pod, Deployment, StatefulSet, DaemonSet, Job, CronJob or anything with spec.template.spec.containers), so there was nothing to check", len(objs))
	}
	return out, workloads, nil
}

type envVal struct {
	known bool
	value string
	why   string
}

// env resolves the container's variables the way the kubelet does for
// $(VAR) references: envFrom first, then env in order.
func (c *container) env() (map[string]*envVal, []string) {
	cms := c.cms
	vars := map[string]*envVal{}
	var unknownSources []string
	for _, it := range asList(c.raw["envFrom"]) {
		m, _ := it.(map[string]any)
		prefix, _ := m["prefix"].(string)
		if ref, ok := m["configMapRef"].(map[string]any); ok {
			name, _ := ref["name"].(string)
			if data, ok := cms[c.at.Namespace+"/"+name]; ok {
				for k, v := range data {
					vars[prefix+k] = &envVal{known: true, value: v}
				}
				continue
			}
			unknownSources = append(unknownSources, "ConfigMap "+name)
			continue
		}
		if ref, ok := m["secretRef"].(map[string]any); ok {
			name, _ := ref["name"].(string)
			unknownSources = append(unknownSources, "Secret "+name)
		}
	}
	for _, it := range asList(c.raw["env"]) {
		m, _ := it.(map[string]any)
		name, _ := m["name"].(string)
		if name == "" {
			continue
		}
		if vf, ok := m["valueFrom"].(map[string]any); ok {
			why := "is set from a fieldRef or resourceFieldRef"
			if ref, ok := vf["configMapKeyRef"].(map[string]any); ok {
				cm, _ := ref["name"].(string)
				key, _ := ref["key"].(string)
				if v, ok := cms[c.at.Namespace+"/"+cm][key]; ok {
					vars[name] = &envVal{known: true, value: v}
					continue
				}
				why = "comes from ConfigMap " + cm + ", which isn't in the input"
			} else if ref, ok := vf["secretKeyRef"].(map[string]any); ok {
				sn, _ := ref["name"].(string)
				why = "comes from Secret " + sn
			}
			vars[name] = &envVal{why: why}
			continue
		}
		v, _ := m["value"].(string)
		t := expand(v, vars, unknownSources)
		if t.unknown {
			vars[name] = &envVal{why: "uses " + t.vars}
		} else {
			vars[name] = &envVal{known: true, value: t.text}
		}
	}
	return vars, unknownSources
}

func asList(v any) []any {
	l, _ := v.([]any)
	return l
}

// expand replaces $(VAR) like Kubernetes does: a defined variable is
// substituted, an undefined one stays as written, and $$ is a $. A variable
// whose value isn't known leaves the argTok marked unknown.
func expand(s string, vars map[string]*envVal, unknownSources []string) argTok {
	t := argTok{}
	if !strings.Contains(s, "$") {
		t.text = s
		return t
	}
	var b strings.Builder
	var missing []string
	for i := 0; i < len(s); i++ {
		if s[i] != '$' || i+1 >= len(s) {
			b.WriteByte(s[i])
			continue
		}
		switch s[i+1] {
		case '$':
			b.WriteByte('$')
			i++
		case '(':
			end := strings.IndexByte(s[i+2:], ')')
			if end < 0 {
				b.WriteString(s[i:])
				i = len(s)
				continue
			}
			ref := s[i+2 : i+2+end]
			raw := s[i : i+3+end]
			if v, ok := vars[ref]; ok {
				if v.known {
					b.WriteString(v.value)
				} else {
					b.WriteString(raw)
					missing = append(missing, "$("+ref+"), which "+v.why)
				}
			} else if len(unknownSources) > 0 {
				b.WriteString(raw)
				missing = append(missing, "$("+ref+"), which envFrom "+strings.Join(unknownSources, ", ")+" may set")
			} else {
				b.WriteString(raw)
			}
			i += 2 + end
		default:
			b.WriteByte(s[i])
		}
	}
	t.text = b.String()
	if len(missing) > 0 {
		t.unknown = true
		t.vars = strings.Join(missing, " and ")
	}
	return t
}

var shells = map[string]bool{"sh": true, "bash": true, "ash": true, "dash": true, "zsh": true, "busybox": true}

// Wrappers that start the real program, and their own flags that take a
// separate value.
var inits = map[string]bool{"tini": true, "tini-static": true, "dumb-init": true, "catatonit": true, "go-runner": true}
var initValueFlags = map[string]bool{"-log-file": true, "--log-file": true, "-p": true, "-l": true, "-e": true, "--rewrite": true, "-r": true}

// argv returns the arguments the program receives, or a reason they can't
// be known from the manifest.
func (c *container) argv(entry []string) ([]argTok, string) {
	vars, unknownSources := c.env()
	var words []string
	for _, a := range c.command {
		words = append(words, a.(string))
	}
	if c.hasCmd {
		for len(words) > 0 && inits[filepath.Base(words[0])] {
			words = words[1:]
			for len(words) > 0 && strings.HasPrefix(words[0], "-") {
				w := words[0]
				words = words[1:]
				if w == "--" {
					break
				}
				if initValueFlags[w] && len(words) > 0 {
					words = words[1:]
				}
			}
		}
		if len(words) == 0 {
			return nil, "its command has no program after the init wrapper"
		}
		if shells[filepath.Base(words[0])] {
			return nil, fmt.Sprintf("its command runs %s, a shell, and flaggap doesn't read shell scripts", words[0])
		}
		words = words[1:]
	} else if !c.hasArgs {
		return nil, "it sets no command or args, so the image's ENTRYPOINT and CMD decide the arguments, and flaggap doesn't read images"
	} else {
		words = append(words, entry...)
	}
	for _, a := range c.args {
		words = append(words, a.(string))
	}
	var out []argTok
	for _, w := range words {
		t := expand(w, vars, unknownSources)
		t.raw = w
		out = append(out, t)
	}
	return out, ""
}
