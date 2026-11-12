---
name: checking-go-flags-in-manifests
description: Checks the command-line flags that Kubernetes manifests (Deployment, StatefulSet, DaemonSet, Job, CronJob or Pod command and args) pass to a Go program against the flags its source really defines, using the program's own flag, spf13/pflag or spf13/cobra parser. Reports flags that crash it at startup ("flag provided but not defined", "unknown flag", "invalid value", "flag needs an argument"), and silent mistakes that start fine but don't do what was written, such as "--debug false" setting debug to true, flags after a positional argument that the flag package never reads, "-port=80" read by pflag as "-p ort=80", deprecated flags and flags set twice. Use when a Go service crash-loops after a chart, image or flag change, when reviewing a Helm chart or manifest change that edits container args, when renaming or removing a flag, or when upgrading the image a chart deploys.
---

# Checking a Go program's flags in Kubernetes manifests

flaggap builds the Go program from source with its flag parsers hooked, runs it with each container's arguments until the moment it parses its flags, reads the exact flag set and the real parser's result, and exits before any of the program's own work. It needs Go 1.22 or newer and the program's source, with its dependencies available (module cache, vendor directory or network).

Tell the user before you run it: it compiles their program and runs its `init` functions and `main` up to the flag parse, in an empty temporary directory with an almost empty environment. Only run it on code the user trusts, normally their own repository.

## Get the two inputs

1. **The program:** the directory of its `main` package, such as `./cmd/server`. If the image is built from a different commit than the working tree (the user is checking a chart against a released image), check out that commit first, for example with `git worktree add /tmp/v1.4.0 v1.4.0`, and point `--pkg` there.
2. **Rendered manifests:** what will actually be applied, never a chart or kustomization directory (flaggap refuses those).
   - Helm: `{ helm template RELEASE CHART -n NS -f values.yaml; echo ---; } > rendered.yaml`. The final `---` marks the file as complete; without it, a file that ends inside a container is refused as possibly cut off.
   - Kustomize: `{ kustomize build DIR; echo ---; } > rendered.yaml`.
   - Plain manifests: the files or the directory.

Check that the render command exited 0 before you go on. Include the ConfigMaps that feed `$(VAR)` references in args, so their values get checked.

## Run it

Build it once, then run it:

    go build -o "${TMPDIR:-/tmp}/flaggap" $(ls "${CLAUDE_PLUGIN_ROOT}"/scripts/*.go | grep -v _test.go)
    "${TMPDIR:-/tmp}/flaggap" --pkg ./cmd/server --container server rendered.yaml

`--container` names the containers that run this program, comma-separated. It can be left out only when the input has one container. To check arguments that aren't in a manifest (a Makefile, a systemd unit, a docker-compose `command`), pass them after `--`: `flaggap --pkg ./cmd/server -- --listen=:8080 --debug`. `--list-flags` prints the flags the program parses (put a cobra subcommand after `--`). If the image's ENTRYPOINT adds words after the program, such as a subcommand, and the container sets only `args`, pass them with `--entrypoint 'serve'`; read the Dockerfile to find out. Add `--format json` for machine-readable output and `--fail-on warn` to fail on warnings too.

## Read the exit code first

- **0:** nothing at or above `--fail-on` (default `error`). Warnings and notes may still be printed; read them.
- **1:** findings at or above `--fail-on`.
- **2:** nothing was checked. The message says why: the program didn't build, didn't reach its flag parse (it crashed, exited or hung first; its output is shown), uses a parser flaggap doesn't know (kingpin, urfave/cli, kong and others), parses only part of its arguments at its first parse, the manifest is empty, a failed command's output, an unrendered template, cut off, or `--container` is missing or wrong. Fix the input and run again. Never describe exit 2 as a clean result.

## Check each finding

- **ERROR, the container won't start:** `unknown-flag`, `bad-value`, `missing-value`, `bad-syntax`, `parse-error` (a custom flag type rejected the value; the real error is quoted), `exits` (`--help`), `unknown-command` (cobra), and `single-dash` when the shorthand's value fails. Each of these was confirmed by the program's real parser.
- **ERROR, it starts but ignores what was written:** `bool-value-ignored` (`--debug false` sets true, `-v 3` on a count flag adds one), `ignored-after-positional` (the flag package stops at the first non-flag argument), `value-is-a-flag` (`--name --debug` makes `--debug` the value), `single-dash` (`-port=80` in pflag).
- **WARN:** `deprecated` (with the program's own message), `repeated` (the last value wins), `after-positional` (the program makes its own flag sets, so later flags may belong to a subcommand flaggap didn't see).
- **note:** `positional` (a cobra command gets positional arguments, which its `Args` check sees only after flaggap stops); values from Secrets, ConfigMaps not in the input, `fieldRef` or `envFrom` that flaggap can't see, and containers whose arguments come from a shell script or the image's CMD.

## Report

1. Errors first: the workload, the container, the argument, the fix line, and the values or template line that produces it.
2. Then warnings, and whether each matters.
3. Say what wasn't checked: the notes, containers not named with `--container`, and that the check used the source at `--pkg`, not the image itself.

## Don't

Don't run it on code the user hasn't asked you to run. Don't change the cluster. Don't say the container will start because flaggap found nothing: it checks flags, not configuration files, environment variables or anything the program does after parsing.
