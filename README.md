# flaggap: check the flags Kubernetes manifests pass to a Go program

[![CI](https://github.com/GillMacedo/flaggap/actions/workflows/ci.yml/badge.svg)](https://github.com/GillMacedo/flaggap/actions/workflows/ci.yml) [![MIT license](https://img.shields.io/badge/license-MIT-blue)](LICENSE)

**A Claude Code skill and Go CLI for teams that run Go programs on Kubernetes. It checks the `command` and `args` in rendered Helm, Kustomize or plain manifests against the flags the program's source really defines, using the program's own `flag`, `spf13/pflag` or `spf13/cobra` parser, and reports flags that crash it at startup and flags it silently ignores.**

A chart and the image it deploys drift apart. The chart adds `--metrics-port` and the image doesn't have it yet, or a release renames `--metrics-addr` to `--metrics-bind-address`, and the pod goes into CrashLoopBackOff with `flag provided but not defined` or `unknown flag`. Other mistakes start fine and do the wrong thing: `--debug` followed by `"false"` sets debug to true, the `flag` package stops at the first argument that isn't a flag and never reads the ones after it, `-port=80` in a cobra program is the shorthand `-p` with the value `ort=80`, and `--timeout=30` fails because Go durations need a unit. `helm lint`, `kubeconform` and schema checks can't see any of this, because to Kubernetes the args are just strings.

flaggap builds the program from source with its flag parsers hooked, runs it with each container's arguments until the moment it parses its flags, and exits before any of the program's own work. It reads the exact flag set the program built, including flags added by libraries such as klog, controller-runtime and the Kubernetes component base, replays the parser's rules over the arguments to find the silent mistakes, and confirms every startup failure with the program's real parser.

## What you can ask Claude

- "This Deployment crash-loops with flag provided but not defined. Which args does the image not know?"
- "We're bumping the operator image to v2. Do the chart's args still work with the new code?"
- "I renamed some flags in cmd/server. Which manifests still pass the old names?"
- "Review the args in this Helm chart for the api and worker containers before I merge."

## Install

As a Claude Code plugin:

    claude plugin marketplace add GillMacedo/flaggap
    claude plugin install flaggap@flaggap-marketplace

Or run it with Go 1.22 or newer, from the repository of the program you're checking. It has no dependencies outside this repository:

    { helm template my-release ./chart; echo ---; } | go run github.com/GillMacedo/flaggap/plugin/scripts@v0.1.0 --pkg ./cmd/server --container server -

## Example

`examples/api` is a made-up service that reads its flags with the standard `flag` package. `broken.yaml` deploys it with a few common mistakes. `--fail-on never` keeps the exit code at 0 for this demo; without it, the run exits 1:

    $ go run ./plugin/scripts --pkg ./examples/api --container api,reindex --fail-on never examples/api/broken.yaml
    ERROR unknown-flag: Deployment shop/api, container api
      --log-format=json is not a flag of github.com/GillMacedo/flaggap/examples/api, so the program exits at startup with "flag provided but not defined: -log-format"
      fix: remove it
      in examples/api/broken.yaml

    ERROR bad-value: Deployment shop/api, container api
      "30" is not a valid duration for -shutdown-timeout (time: missing unit in duration "30"), so the program exits at startup. The manifest writes it as --shutdown-timeout=$(GRACE)
      fix: add a unit, such as 30s
      in examples/api/broken.yaml

    ERROR bool-value-ignored: Deployment shop/api, container api
      --debug is a boolean flag, which never takes the next argument as its value: --debug sets it to true and "false" is read as a positional argument, where the parser stops reading flags
      fix: write --debug=false
      in examples/api/broken.yaml

    ERROR ignored-after-positional: Deployment shop/api, container api
      the parser stops at the first argument that isn't a flag, "false", so --workers=8 is never parsed and the program runs with its default
      fix: move the flags before "false", or remove it
      in examples/api/broken.yaml

    ERROR ignored-after-positional: CronJob shop/api-reindex, container reindex
      the parser stops at the first argument that isn't a flag, "reindex", so -workers=2 -tag=nightly are never parsed and the program runs with their defaults
      fix: move the flags before "reindex", or remove it
      in examples/api/broken.yaml

    5 errors, 0 warnings and 0 notes in 2 checked containers. github.com/GillMacedo/flaggap/examples/api: 6 flags, parsed with the flag package.

`examples/worker` is a made-up cobra program with a vendored cobra and pflag, so it builds offline:

    $ go run ./plugin/scripts --pkg examples/worker --fail-on never examples/worker/broken.yaml
    ERROR single-dash: Deployment jobs/worker, container worker
      pflag reads one dash as one-letter shorthands, so -concurrency=8 isn't --concurrency: it gives -c (--concurrency) the value "oncurrency=8", which isn't a valid whole number (invalid syntax), and the program exits at startup
      fix: write --concurrency=8
      in examples/worker/broken.yaml

    ERROR bad-value: Deployment jobs/worker, container worker
      "30" is not a valid duration for --poll-interval (time: missing unit in duration "30"), so the program exits at startup
      fix: add a unit, such as 30s
      in examples/worker/broken.yaml

    ERROR unknown-flag: Deployment jobs/worker, container worker
      --metrics-addr=:9090 is not a flag of example.com/worker, so the program exits at startup with "unknown flag: --metrics-addr"
      fix: remove it, or did you mean --metrics-bind-address?
      in examples/worker/broken.yaml

    ERROR bool-value-ignored: Deployment jobs/worker, container worker
      --dry-run is a boolean flag, which never takes the next argument as its value: --dry-run sets it to true and "false" is read as a positional argument
      fix: write --dry-run=false
      in examples/worker/broken.yaml

    WARN deprecated: Deployment jobs/worker, container worker
      --threads is deprecated: use --concurrency instead
      in examples/worker/broken.yaml

    4 errors, 1 warning and 0 notes in 1 checked container. example.com/worker: 9 flags, parsed with pflag.

The same Deployment with the arguments fixed stays quiet:

    $ go run ./plugin/scripts --pkg examples/worker examples/worker/fixed.yaml
    No problems in 1 checked container. example.com/worker: 9 flags, parsed with pflag.

Arguments from somewhere else, such as a Makefile, a systemd unit or a docker-compose `command`, go after `--`:

    $ go run ./plugin/scripts --pkg ./examples/api --fail-on never -- -listen :9090 serve -workers 8
    ERROR ignored-after-positional: command line
      the parser stops at the first argument that isn't a flag, "serve", so -workers is never parsed and the program runs with its default
      fix: move the flags before "serve", or remove it

    1 error, 0 warnings and 0 notes in the arguments. github.com/GillMacedo/flaggap/examples/api: 6 flags, parsed with the flag package.

`--list-flags` prints the flags the program parses, for the subcommand after `--` if there is one:

    $ go run ./plugin/scripts --pkg examples/worker --list-flags -- run
    example.com/worker reads 9 flags with pflag:
      --concurrency, -c                  int       default "4"
      --dry-run                          bool      default "false"
      --help, -h                         bool      default "false"
      --log-level                        string    default "info"
      --metrics-bind-address             string    default ":8080"
      --poll-interval                    duration  default "5s"
      --queue, -q                        string    default "default"
      --threads                          int       default "0" (deprecated)
      --v, -v                            int       default "0"

Add `--format json` for machine-readable output.

## How it works

1. `go list` finds the main package and its dependencies. A program whose own module imports a parser flaggap doesn't model (kingpin, urfave/cli, kong, go-flags, ff and others) is refused, and so is one whose module imports none of `flag`, `pflag` or `cobra`.
2. `go build -overlay` adds a hook in front of `(*FlagSet).Parse` in the standard `flag` package and in `pflag`. pflag in the module cache can't be overlaid, so flaggap copies it to a temporary directory and builds with a copy of your `go.mod` (or `go.work`) that replaces it. Your files are never changed.
3. For each container, the program runs with the container's arguments in an empty temporary directory, with only `PATH`, `HOME` and `TMPDIR` set. At the first `Parse` call that reads those arguments (or cobra's), the hook writes the flag set, runs the real parser with errors returned instead of printed, and exits.
4. flaggap replays the parser over the arguments with that flag set. Every failure it predicts must match the real parser's error, or it refuses with exit 2 and asks for a bug report. After a confirmed failure it removes those arguments and runs again, so it finds every failure and not only the first.

## When it refuses

flaggap exits 2 and prints only the reason when it can't trust what it read, whatever `--fail-on` says. For example, feeding it the error that `helm template` prints when a chart fails to render:

    printf 'Error: execution error at (worker/templates/deployment.yaml:31:20): image.tag is required\n' | go run ./plugin/scripts --pkg examples/worker -

prints this on stderr. The program exits 2 (under `go run`, Go adds a line `exit status 2` and itself exits 1):

    flaggap: can't trust the input, nothing was checked: stdin, document 1: not valid YAML or JSON (yaml: mapping values are not allowed in this context); is it the output of a failed command?

On the manifest side it does the same for empty files, other failed command output, a values file, an API error, invalid or cut-off YAML or JSON, a file with no final line break, a template with `{{ }}` left in, a chart or kustomization directory, a workload without containers, a container without a name or image, args that aren't strings, and a file whose last object ends inside a container. A single bad file among good ones fails the whole run. On the program side it refuses a package that doesn't build, a package that isn't `main`, and a program that crashes, exits or is still running after `--timeout` (20 seconds by default) before it parses its flags (the program's output is shown), and a program whose first flag parse reads only part of its arguments. It also refuses when no selected container has arguments it can read, so a run that checked nothing never looks clean.

A manifest cut on a line break can still parse. If the cut falls inside a container, the args after it are gone, so flaggap refuses a file whose last object ends inside a container. A rendered file that really ends there, which is common for `helm template` output, can be marked complete with a final `---` line, as in the install example. A file cut exactly between two objects is a valid shorter file, and no tool can tell. The summary line says how many containers were checked, so check that number.

## What it reports

| Code | Severity | Meaning |
|---|---|---|
| `unknown-flag` | ERROR | Not a flag of the program: "flag provided but not defined" or "unknown flag". The fix suggests a close name, or one with the same words (`--metrics-addr` and `--metrics-bind-address`) |
| `bad-value` | ERROR | A value the flag's type rejects: `30` for a duration, `yes` for a bool, `1k` for an int, a value a custom flag type rejects |
| `missing-value` | ERROR | The last argument is a flag that needs a value |
| `bad-syntax` | ERROR | `---x`, `-=x` and similar |
| `exits` | ERROR | `-h` or `--help`, which print usage and exit |
| `unknown-command` | ERROR | cobra exits with "unknown command" before it parses flags |
| `parse-error` | ERROR | The real parser rejected the arguments in a way the replay didn't classify; its error is quoted |
| `single-dash` | ERROR | `-port=80` in pflag is the shorthand `-p` with the value `ort=80`: it fails, or sets a different value than written |
| `bool-value-ignored` | ERROR | `--debug false`: a bool flag never takes the next argument, so it is set to true and `false` becomes a positional argument. The same for a pflag count flag followed by a number (`-v 3`) |
| `ignored-after-positional` | ERROR | The `flag` package (or pflag with interspersed flags off) stops at the first argument that isn't a flag, so later flags are never read |
| `value-is-a-flag` | ERROR | `--name --debug`: the flag takes `--debug` as its value |
| `deprecated` | WARN | A pflag flag marked deprecated, with the program's message |
| `repeated` | WARN | The same single-value flag set twice; the last one wins |
| `after-positional` | WARN | Like `ignored-after-positional`, in a program that makes its own `flag.FlagSet`s, so the later flags may belong to a subcommand flaggap didn't see |
| `positional` | note | A cobra command gets positional arguments; cobra checks them with the command's `Args` after parsing flags, where flaggap stops |
| `not-checked` | note | A value from a Secret, a ConfigMap not in the input, a `fieldRef` or an unreadable `envFrom`, or a container whose arguments come from a shell or the image |

Arguments are built the way the kubelet builds them: `command[1:]` followed by `args`, or `args` alone when there is no `command`; `$(VAR)` is replaced from the container's `env` and `envFrom` (ConfigMaps in the input are read), an undefined variable stays as written, and `$$` is a literal `$`. A `command` that starts with `tini`, `dumb-init`, `catatonit` or `go-runner` is unwrapped. A `command` that runs a shell is not checked.

## Exit codes

| Code | Meaning |
|---|---|
| 0 | Nothing at or above `--fail-on`, which is `error` by default. Use `--fail-on warn` to fail on warnings too, or `never`. |
| 1 | Findings at or above `--fail-on`. |
| 2 | The input can't be trusted or the flags are wrong. Nothing was checked, and stdout is empty. |

## Limits

- **It runs your program.** Package `init` functions and `main` run until the flag parse. That is usually setup code, but a program that connects to a database or reads files before parsing its flags will try to. Run it only on code you trust.
- **Source, not the image.** flaggap checks the code at `--pkg`. If the image was built from another commit, check that commit out first. It doesn't read images, so the image's `ENTRYPOINT` and `CMD` are not used: a container without `command` is assumed to run the program directly with its `args`. If the image's `ENTRYPOINT` adds words such as a subcommand, give them with `--entrypoint 'serve'`.
- **One subcommand path per container.** A cobra program is checked with the subcommand its arguments select. A program that makes its own `flag.FlagSet` for subcommands gets a warning for the flags after the subcommand name.
- **Parsers.** Only the standard `flag` package, `spf13/pflag` and `spf13/cobra`. Programs that read `os.Args` by hand, or parse arguments in a dependency flaggap doesn't know, are refused.
- **Flags only.** A clean result means the arguments parse as written. It doesn't check what the program does with the values, its environment variables or its config files.
- **Linux builds.** The program is built for Linux (unless `GOOS` is set) and the architecture of the machine running flaggap, so it can run. Flags defined only in files for other platforms are not seen.

## FAQ

**Why does my pod say "flag provided but not defined"?** The program, which uses Go's `flag` package, got a flag it doesn't define. Usually the chart or manifest was written for a different version of the image. flaggap names the flag and suggests the closest one the program has.

**Why is `--debug false` wrong?** In both Go's `flag` package and pflag, a boolean flag doesn't take the next argument as its value. `--debug false` sets debug to true and passes `false` as a positional argument; the `flag` package then stops reading flags. Write `--debug=false`.

**Why does `-port=80` fail in a cobra program?** pflag reads a single dash as a group of one-letter shorthands, so `-port=80` is `-p` with the value `ort=80`. Write `--port=80`. Go's `flag` package accepts one or two dashes.

**Does it need a cluster or Docker?** No. It needs Go and the program's source with its dependencies (module cache, `vendor/` or network).

**Is it an MCP server?** No. It's a skill with one Go program, so `go run` or `go build` is all it needs.

## Tested on

- **The real parsers:** `tests/realparser_test.go` gives four programs (the standard `flag` package with every built-in flag kind and custom `Var`, `Func`, `BoolFunc` and `TextVar` flags; pflag v1.1.0 with shorthands, counts, slices, IPs and a deprecated flag; pflag with interspersed flags off; and a cobra v1.10.2 program with Go flags added) 30 random argument lists each per run, plus pinned cases, and compares flaggap's report with what the unhooked program does: when the program fails to start, flaggap reports an error; when it starts, flaggap reports no startup failure; when it leaves a flag unparsed, flaggap reports an error. During development I ran it with 150 lists per program (600 in all, 210 that started and 390 that didn't) with no disagreement.
- **Real programs, by hand:** metrics-server v0.7.2 (cobra and the Kubernetes component base): the flags flaggap reads match `--help` of the real binary except `--help` itself and one hidden deprecated flag, its own `manifests/base/deployment.yaml` and the release manifests of v0.3.6 to v0.6.0 stay quiet, and `--logtostderr` and `--etcd-servers`, which libraries in the binary define but the binary rejects, are reported. aws/eks-pod-identity-agent issue 40, where the chart passed `--metrics-port` to an image that didn't have it: with the chart rendered by Helm v3.16.3 with `metrics.enabled=true`, the source from before commit dbfb48f gives `unknown flag: --metrics-port`, the same error as in the issue, and v0.1.20 and the default render stay quiet. ingress-nginx controller-v1.11.3 (pflag with Go flags and `$(POD_NAMESPACE)` in args) stays quiet on its own `deploy.yaml` and on those of controller-v0.35.0, v0.49.3 and v1.0.0. Kubebuilder's `testdata/project-v4` from v4.3.1 (the `flag` package with controller-runtime's zap flags) stays quiet on its `config/manager/manager.yaml`, reports the kubebuilder v2 arguments `--metrics-addr` and `--enable-leader-election` with the new names as fixes, and quotes zap's own error for `--zap-log-level=verbose`. external-dns v0.15.0 is refused because it uses kingpin. These checks need network and aren't part of `make test`.
- **Input it can't trust:** tests pass 18 kinds of bad manifest alone, on stdin, before and after a real file, and in a directory with a real file, and 12 kinds of bad program source (empty, random text, binary, cut mid-file, cut on a line, build and vet output, compile and type errors, a package that isn't `main`, no flag parsing, an unknown parser), plus programs that panic, exit or hang before parsing, under every `--fail-on` and `--format json`, and check for exit 2 with nothing on stdout. Others cut the example manifests at every byte: a mid-line cut always exits 2, and a line-break cut exits 2 or gives a subset of the whole file's findings.
- **Builds:** the module-cache and `go.work` paths are tested offline with a module cache filled from a file-based `GOPROXY`, and check that `go.mod` and `go.work` are unchanged. The installed plugin is copied out of the repository and built with the skill's own command.
- **Go versions:** the tests pass on Go 1.22.12 and Go 1.27.3 on Linux arm64.

Not tested: macOS and Windows; programs that use `pflag` forks or vendored copies under another import path; a fresh Claude Code session using the skill. The `go run ...@v0.1.0` line works only once the repository is published.

## Contributing

Contributions are welcome, from people and from people working with AI agents. See [CONTRIBUTING.md](CONTRIBUTING.md). Good first issues are [listed here](https://github.com/GillMacedo/flaggap/issues?q=is%3Aissue+is%3Aopen+label%3A%22good+first+issue%22).

### How pull requests work

1. **Open a PR.** Fork the repo, work on a branch, and keep it to one change. Write a title that says what changes for users, because it becomes the changelog line: `feat:` goes under Added and `fix:` under Fixed.
2. **Automatic check.** Code changes need tests, and `make test` must pass. Changes over 30 lines need a linked open issue (`Closes #<number>`), and a PR can have at most 800 changed lines. If something is missing, a comment tells you what to fix.
3. **Review.** Claude reviews each PR automatically. Code PRs also get a security review and are tested in a sandbox. The review then merges the PR or asks for changes, and a small problem may become a follow-up issue instead. A PR with no new commit 14 days after a change request is closed, and you can reopen it. PRs that change CI, agent settings, npm or Python dependencies, vendored code or build files can't be merged this way, and a comment says so. The same goes for anything the security review doesn't clear.
4. **Release.** A merge that changes what users install becomes a new version within about two hours, with a GitHub Release and, if the project has one, its npm package. Docs and CI changes wait for the next release.

## AI disclosure

This project was largely generated with Claude Code and reviewed by its maintainer.

## About me

I'm Giulliano Macedo, and I build developer tools at GoTech Solutions. Follow along or get in touch:

- GitHub: [GillMacedo](https://github.com/GillMacedo)
- LinkedIn: [giulliano](https://www.linkedin.com/in/giulliano/)
- X: [@giumacedo](https://x.com/giumacedo)

If `flaggap` saved you some prompts, a star helps others find it.

---

Built by [Giulliano Macedo](https://github.com/GillMacedo), GoTech Solutions · [LinkedIn](https://www.linkedin.com/in/giulliano/) · [X](https://x.com/giumacedo)
