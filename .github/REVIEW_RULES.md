# Review rules for Thirai

The AI reviewer and human maintainers check every pull request against these rules.

## Security (blocking)

- Every request except the page shell must check the token before reading the body or doing anything. Do not add an endpoint that skips the check.
- The phone may only trigger actions that exist in `config.json`, by button number. A change must not let the phone send its own keystrokes, shell commands, file paths, URLs to open or text to type, beyond what the config already allows.
- App launches stay limited to apps the server itself listed.
- No new listeners on other ports or interfaces, no port forwarding, UPnP or remote access, and no telemetry or calls to outside services, unless the PR says why and a maintainer agrees. Twitch and OBS calls stay on their documented APIs.
- Untrusted text (track names, app names, chat, Twitch data) is written to the page with `textContent`, never `innerHTML`.
- Request bodies keep a size cap, checked after the token.
- No secrets, tokens, passwords, client secrets or personal paths in code, docs, screenshots or tests. Twitch uses the device-code flow with a public client ID that the user supplies.
- Changes to `.github/` (workflows, the reviewer, these rules) are security-sensitive. Workflows that use `pull_request_target` must never check out or run code from the pull request.

## Compatibility (blocking)

- The phone page (`windows/web/index.html` and `Sources/thirai/web/index.html`) stays plain ES5 so iOS 8/9 Safari and old Android browsers run it: no arrow functions, `let`/`const`, template strings, classes, `fetch`, promises or `async`. CI parses it as ES5.
- The two copies of `index.html` stay identical.
- The Windows server builds with Go 1.22 on the standard library only. A new dependency needs a reason in the PR.
- Go code is `gofmt`-formatted and passes `go vet`.

## Content (blocking)

- No third-party logos, product photos or brand icons bundled in the repo. Button icons are simple SVGs drawn for the project. Brand names may appear as plain text labels.
- No copied code or assets from other deck apps or vendor SDKs.
- Keep the credit to OldPhoneDeck by GLAUCOMA82 and both copyright lines in `LICENSE`.

## Quality (ask, don't block)

- One change per PR. Unrelated refactors go in their own PR.
- New buttons or settings are documented in `docs/GUIDE.md`.
- Errors shown to the user say what to do next.
- Code stays small and readable; prefer the existing patterns in the file being changed.
