# Contributing

Thanks for helping. Fork the repo, make your change on a branch, and open a pull request. Only the maintainer merges to `main`.

## Setup

    make test
    make lint

You need Go 1.22 or newer. `make test` builds small programs in `tests/testdata` and `examples/` with the real `flag` package and vendored pflag and cobra, and compares flaggap with what they really do, offline. CI runs the tests on Go 1.22 and on the current release. `make lint` also needs Claude Code. The dev container in `.devcontainer/` sets up both.

## Pull requests
- PRs go through an automatic check before a person reads them: changes over 30 lines need a linked open issue (`Closes #<number>`), code changes need tests, `make test` must pass, and a PR can have at most 800 changed lines.
- Keep each one to a single change, with tests.
- A new or changed finding needs a case in `tests/realparser_test.go` that runs a real program built with the parser it describes, and a case that must stay quiet. Link the `flag`, pflag or cobra source it follows.
- Each new kind of input needs a test that broken input exits 2.
- Update `ROADMAP.md` and docs when relevant.
- Releases are automatic: a merge that changes what users install becomes a new version with its changelog line taken from the PR title, so write a title that says what changed for users. Edit `CHANGELOG.md` yourself only if you want different wording. Docs and CI changes wait for the next one.

## Working with AI agents
Agents are welcome. `CLAUDE.md` and `AGENTS.md` have the project's instructions, and `.claude/commands/` has common tasks. Say in your PR that an agent helped, and review the result yourself before submitting. You're responsible for what you submit.

By contributing, you agree that your work is licensed under the MIT License.
