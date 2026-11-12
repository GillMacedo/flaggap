# Security

Report vulnerabilities through [GitHub's private vulnerability reporting](https://github.com/GillMacedo/flaggap/security/advisories/new), not in a public issue.

`flaggap` compiles the Go program you point it at and runs it until the program parses its flags: package `init` functions and `main` up to that point run on your machine, in an empty temporary directory with only `PATH`, `HOME` and `TMPDIR` set, and are stopped after `--timeout`. Run it only on code you trust, as you would `go test`. It never connects to a cluster. It doesn't read Secrets: arguments that use a Secret are listed as not checked, and their values are never printed.

A missed or wrong finding is a normal bug. If `flaggap` changes your `go.mod`, `go.work` or any file outside its temporary directory, prints a Secret value, or reports a clean result for input it couldn't read, report it privately. `flaggap` is a diagnostic aid and doesn't enforce anything.
