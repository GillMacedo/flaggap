# Changelog

All notable changes are documented here, in [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) format. This project uses [Semantic Versioning](https://semver.org/).

## [Unreleased]

## [0.1.0] - 2026-11-12

### Added
- Checks the `command` and `args` of containers in rendered Kubernetes manifests, or arguments given after `--`, against the flags a Go program defines, for programs that use the standard `flag` package, `spf13/pflag` or `spf13/cobra`.
- Reads the program's exact flag set by building it with `go build -overlay` hooks in `(*FlagSet).Parse` and running it until it parses its flags, and confirms every startup failure with the program's real parser.
- Reports unknown flags, values the flag's type rejects, missing values, bad syntax, `--help`, cobra's unknown commands, `-port=80` in pflag, `--debug false`, flags after a positional argument, flags that take the next flag as their value, deprecated flags and flags set twice.
- Builds arguments the way the kubelet does: `command` and `args`, `$(VAR)` expansion with `env`, `envFrom` and ConfigMaps in the input, and `tini`, `dumb-init`, `catatonit` and `go-runner` wrappers.
- Exit 2 with no result for input it can't trust: failed builds, programs that don't reach their flag parse, unknown parsers, empty or failed-command manifests, unrendered templates and files cut inside a container.
- Text and JSON output, `--fail-on error|warn|never`, `--container`, `--entrypoint`, `--list-flags` and `--timeout`.
- `checking-go-flags-in-manifests` skill and plugin manifest.
