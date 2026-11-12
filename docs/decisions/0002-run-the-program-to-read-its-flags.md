# 0002: Run the program up to its flag parse

**Decision:** flaggap builds the program with hooks in `(*FlagSet).Parse` of `flag` and `pflag` and runs it until it parses its flags, instead of reading flag definitions from source.

**Why:** the static approach was built first and tested on metrics-server. It listed 62 flags the binary doesn't accept, because libraries such as klog and the Kubernetes API server options register flags on sets the program never parses, and it missed 23 the binary does accept, because their names are built at run time. The first problem turns a real crash into a clean result, which is the failure this tool exists to prevent. Following flag sets through the program statically would need a whole-program data-flow analysis that would still miss cases. The hook sees the set the parser uses.

**Cost:** the program's `init` functions and `main` run until the parse, so the user must trust the code, as with `go test`. The skill tells Claude to say so. A program that does work before parsing its flags, such as connecting to a database, may fail or hang; flaggap then refuses with the program's output and never reports a clean result. A build takes as long as `go build`.
