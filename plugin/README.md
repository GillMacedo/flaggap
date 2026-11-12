# flaggap plugin

This plugin adds the `checking-go-flags-in-manifests` skill to Claude Code. The skill runs `scripts/flaggap.go`, which builds a Go program with its flag parsers hooked, runs it with each container's arguments from rendered Kubernetes manifests up to the moment it parses its flags, and reports flags it rejects at startup or silently ignores. It needs Go 1.22 or newer and the program's source. It never connects to a cluster.

See the repository README for examples, exit codes and limits: https://github.com/GillMacedo/flaggap

## Install

    claude plugin marketplace add GillMacedo/flaggap
    claude plugin install flaggap@flaggap-marketplace
