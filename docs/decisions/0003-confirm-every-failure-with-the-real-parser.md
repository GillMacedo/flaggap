# 0003: Confirm every startup failure with the real parser

**Decision:** every finding that says the program exits is checked against the error of the program's real parser, run on the same arguments inside the hooked program. A disagreement exits 2.

**Why:** the replay in `argv.go` is needed for the mistakes the real parser accepts (`--debug false`, flags after a positional argument, `-port=80` with a string shorthand), but a replay can be wrong, and parsers change between versions (pflag v1.1.0 changed its error text for numbers). Checking each predicted failure against the real error means a replay bug shows up as a refusal, never as a wrong answer. Custom flag types, such as zap's log level or Kubernetes feature gates, are judged only by the real parser.

**Cost:** each failure needs another run of the program, up to 30 per container.
