# Security policy

Thirai lets a phone on your network control your computer, so security reports are welcome.

## Reporting a problem

Report it privately through GitHub: open the **Security** tab of this repo and choose **Report a vulnerability**. Please don't open a public issue or pull request for it.

Include what you found, how to reproduce it, and which version or commit you tested. You'll get a reply within a week.

## Scope

In scope: the Windows server (`windows/`), the Mac server (`Sources/thirai/`), the phone page, and the GitHub workflows in `.github/`.

Known limits, already documented in `docs/GUIDE.md`: the Windows server starts in USB mode and only listens on the PC itself. In the opt-in Wi-Fi mode, and on the Mac server, traffic is plain HTTP on your local network, so anyone who can watch that network can read the token. Thirai is meant for a home network, never for port forwarding or public Wi-Fi.
