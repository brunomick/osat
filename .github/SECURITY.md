# Security policy

osat handles credentials and remote access, so security reports are taken seriously.

## Reporting a vulnerability

Please **don't open a public issue**. Report it privately through GitHub: [Report a vulnerability](https://github.com/brunomick/osat/security/advisories/new) (Security tab → Advisories).

Include what's affected, how to reproduce it and the impact you see. You'll get an answer within a week, and a fix or a plan as soon as the problem is confirmed. Credit is given in the release notes unless you'd rather stay anonymous.

## Supported versions

Fixes go into the latest release. Please check that the problem still happens there before reporting.

## Scope

Examples of what's in scope: credentials ending up outside the system keyring or in the log, host key or RDP certificate checks that can be bypassed, the Claude Code bridge or the built-in browser being reachable from outside `127.0.0.1` or by another website, path traversal when extracting archives or serving folders, and weaknesses in the `.osat` export encryption.
