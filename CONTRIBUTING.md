# Contributing to Skimdit

Thank you for helping. Skimdit is a one-person project, so please read this before opening an issue or pull request.

## Questions

Ask questions in [GitHub Discussions](https://github.com/GTKottman/skimdit/discussions), not in issues.

## Issues

- **Bugs** need the Skimdit version (`skimdit version`), your OS, and steps that reproduce the problem.
- **Feature requests** need a real use case. Check the non-goals in the README first.
- Security problems go through [SECURITY.md](SECURITY.md), never a public issue.

## Pull requests

- Open an issue first for anything bigger than a small fix, so we can agree on the approach.
- Run `gofmt -l .`, `go vet ./...` and `go test ./...` before pushing. CI runs them on Linux, macOS and Windows.
- Add or update tests for every change in behaviour.
- Keep the theme and plugin interfaces stable. A breaking change needs an `engine_api` bump and a deprecation warning one release ahead.

## Sign your commits (DCO)

Every commit must carry a `Signed-off-by` line, which certifies the [Developer Certificate of Origin](https://developercertificate.org/):

```sh
git commit -s -m "Fix feed date format"
```

By signing off you confirm you wrote the change, or have the right to submit it, under the project's licence (AGPL-3.0-or-later).

## The maintainer may say no

Skimdit stays small on purpose. A pull request can be declined because it adds scope, maintenance cost or risk, even when the code is good. That is not a judgement of you or your work.
