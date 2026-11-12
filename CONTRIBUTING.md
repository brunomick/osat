# Contributing to osat

Thanks for helping. Bug reports, ideas and pull requests are all welcome, in English or Spanish.

## Reporting a bug

Open an [issue](https://github.com/brunomick/osat/issues/new/choose) with:

- what you did, what you expected and what happened instead;
- your osat version and operating system;
- the diagnostic info from **Settings → Copy diagnostic information** (version, system, data folder and the last lines of the log; it never contains passwords, key passphrases or tokens, but check it before pasting).

Security problems go through [SECURITY.md](SECURITY.md), not public issues.

## Setting up

You need Go 1.25+, Node.js 18+ and the Wails CLI v2.16 (`go install github.com/wailsapp/wails/v2/cmd/wails@v2.16.0`). Platform requirements are in [docs/desarrollo.md](docs/desarrollo.md#requisitos).

```bash
wails dev                 # app with hot reload (Linux: wails dev -tags webkit2_41)
go test ./internal/...    # backend tests
cd frontend && npm test   # frontend tests (Vitest)
cd frontend && npm run check
```

The full reference (project layout, design notes, end-to-end tests) is in [docs/desarrollo.md](docs/desarrollo.md) (Spanish).

## Pull requests

- Keep each PR to one change, and explain the why in the description.
- Match the existing style; comments and commit messages in the codebase are in Spanish, and either language is fine for yours.
- Add or update tests for what you change, and make sure `go test ./internal/...`, `npm test` and `npm run check` pass.
- User-facing text goes through i18n: add the key to both `frontend/src/lib/i18n/en.json` and `es.json` (or `internal/i18n` for text built in Go).
- If you add, remove or update a dependency: `go mod tidy` for Go, and update `THIRD_PARTY_NOTICES.md`.

## License

By contributing you agree that your work is distributed under the [GPL-3.0-or-later](LICENSE), like the rest of osat.
