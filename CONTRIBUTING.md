# Contributing

Small, focused pull requests are easiest to review. Open an Issue first for protocol, schema, installer, or public-API changes. Do not use a public Issue for security vulnerabilities; follow `SECURITY.md`.

Before submitting:

1. Base the change on `main`; do not rewrite published tags.
2. Add regression tests and update Chinese/English user-facing documentation together.
3. Run `go test -race ./...`, `go vet ./...`, `staticcheck ./...`, the Python installer tests, and `npm ci && npm test -- --run && npm run build` under `web/` when relevant.
4. Run `bash scripts/check-sensitive-files.sh` and `git diff --check`.
5. Describe compatibility, migration, rollback, and privacy impact.

Never commit `.env`, runtime data, credentials, private infrastructure details, generated build output, or unredacted logs. Use documentation IP ranges (`192.0.2.0/24`, `198.51.100.0/24`, `203.0.113.0/24`, `2001:db8::/32`) in examples.
