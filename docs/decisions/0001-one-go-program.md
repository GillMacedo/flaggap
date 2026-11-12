# 0001: One Go program in a skill, not an MCP server

**Decision:** flaggap is a skill with one Go program built from `plugin/scripts/*.go`, using only the standard library and a copied YAML parser.

**Why:** an agent can run a command and read its output, which is all this needs. A skill installs with two commands and runs with the Go toolchain the user already needs to build their program. No go.mod means the installed plugin builds with `go build` on its files alone.

**Cost:** the YAML parser is copied in (`yaml_*.go`, Apache-2.0 and MIT, with its notice) and has to be updated by hand.
