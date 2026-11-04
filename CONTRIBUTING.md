# Contributing to Thirai

Thanks for helping. Changes come in through pull requests from your own fork. Nobody pushes to `main` directly, including maintainers.

## How to send a change

1. Fork this repo on GitHub (the Fork button, top right).
2. Clone your fork and make a branch: `git checkout -b my-change`.
3. Make one focused change and test it (see below).
4. Push the branch to your fork and open a pull request into `main` here.

Pull requests opened from a branch inside this repo fail the fork check. Open them from your fork.

## What runs on your pull request

- **CI** builds the Windows server for x64 and arm64, runs `gofmt` and `go vet`, checks that the two copies of the phone page match, and checks that the phone page is plain ES5.
- **Fork check** confirms the pull request comes from a fork and targets `main`.
- **AI review** reads the change and posts one comment against [the review rules](.github/REVIEW_RULES.md). It only suggests. A maintainer reviews and merges.

The first time you contribute, a maintainer has to approve the workflows before they run. That's a GitHub setting for first-time contributors.

## Testing your change

- Windows server: from `windows/`, run `go build` on Windows, or `go run .` on macOS or Linux for a demo server with sample data.
- Phone page: open the printed link on a phone, or in a desktop browser's device mode. If you can, try an old device too.
- The phone page lives in `windows/web/index.html`. Copy it to `Sources/thirai/web/index.html` after editing.

## Ground rules

- Keep the phone page ES5. Old iPhones and Android browsers depend on it.
- No secrets, tokens or personal paths in commits.
- No third-party logos or brand icons in the repo.
- Report security problems privately, as described in [SECURITY.md](SECURITY.md), not in a public issue.
