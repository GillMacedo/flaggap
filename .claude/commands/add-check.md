Add a check to flaggap for: $ARGUMENTS

1. Find the code in Go's `flag` package, `spf13/pflag` or `spf13/cobra` that defines the behavior, and link it in the PR.
2. Add it to `checkStd` or `checkPflag` in `plugin/scripts/argv.go`, with a code in kebab case, a message that says what the program does with the arguments, and a fix line. A finding that says the program exits must call `fail` with the text the real parser's error contains.
3. Add a case to `TestRealParserCases` in `tests/realparser_test.go`, and words to the target's vocabulary so the random test draws it, plus a case that must stay quiet.
4. Add the code to the table in `README.md` and to the skill's list in `plugin/skills/checking-go-flags-in-manifests/SKILL.md`.
5. Run `make test`.
