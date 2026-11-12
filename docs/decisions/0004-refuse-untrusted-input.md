# 0004: Refuse input it can't trust, with exit 2

**Decision:** flaggap exits 2 and prints nothing on stdout for manifests it can't fully read (empty files, failed command output, unrendered templates, charts, kustomizations, files with no final line break, files whose last object ends inside a container) and for programs it can't read (a failed build, a package that isn't `main`, an unknown parser, a program that exits, crashes or hangs before it parses its flags), and when no selected container's arguments could be read. `--fail-on` doesn't change this.

**Why:** a check that passes on the output of a failed `helm template`, or on a program that never reached its flag parse, is worse than no check. A manifest cut on a line break inside a container drops the args after the cut, which turns a finding into a clean result.

**Cost:** `helm template` output often ends inside a container and is refused until it gets a final `---`. The message and the README say so.
