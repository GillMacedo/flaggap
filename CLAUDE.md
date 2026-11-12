# flaggap

Claude Code skill and Go CLI that checks the command-line flags Kubernetes manifests pass to a Go program against the flags its source defines, using the program's own `flag`, `spf13/pflag` or `spf13/cobra` parser.

## Architecture
`plugin/scripts/flaggap.go` holds the CLI, the report and `checkArgs`, which runs the program, replays the parser and checks each predicted failure against the real one. `plugin/scripts/program.go` builds the program with `go build -overlay` hooks (`patchParse`, `stdHook`, `pflagHook`; `copyPflag` and `workspaceReplace` for pflag in the module cache), runs it (`binary.run`) and reads what the hook wrote (`parseDump`). `plugin/scripts/argv.go` replays the parsers (`checkStd`, `checkPflag`). `plugin/scripts/manifest.go` loads manifests, refuses input it can't trust, and builds each container's arguments the way the kubelet does. `plugin/scripts/yaml_*.go` is go.yaml.in/yaml/v2 copied in, so the plugin builds with no go.mod. `plugin/skills/checking-go-flags-in-manifests/SKILL.md` tells Claude how to use it. See `docs/architecture.md`.

## Commands
- `make test`: gofmt, vet and tests, including `tests/`, which compare flaggap with real programs built with the real parsers
- `make lint`: plugin manifest validation (needs Claude Code)
- `make demo`: run on the examples

## Conventions
- Standard library only, plus the copied YAML parser. Never connect to a cluster. Never write outside the temporary directory.
- Any input it can't fully read or trust exits 2 with nothing on stdout, whatever `--fail-on` says. Add a case to `badManifests` or `TestBadPrograms` in `tests/inputs_test.go` for each new kind of input.
- A finding that says the program exits must be confirmed by the real parser's error (`fail` with the error text). If the replay and the real parser disagree, refuse; never report a clean result.
- Tests must not assume the machine they run on: build for the host, and read `runtime` for anything platform-specific.
- Never print Secret values.

## Do and don't
- Do run `make test` on Go 1.22 and on the current Go release before calling a change done.
- Don't add `go.mod` requirements to the root module: the installed plugin has no module. Test programs that need pflag or cobra live in `examples/worker` and `tests/testdata/pflagmod`, each with its own `vendor/`.
- Don't print "No problems" for a run that checked nothing.

## Definition of done
`make test` and `make lint` pass, README output matches a real run, and `ROADMAP.md` and docs are updated when relevant.
