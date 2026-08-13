# DocReview Agent Repository Guide

## Scope and structure

- `apps/server/`: Go backend, HTTP entrypoints, agents, workflows, storage, and tests.
- `apps/web/`: Next.js frontend and Vitest tests.
- `config/`: checked-in runtime defaults; secrets do not belong here.
- `deploy/`: production Compose and environment-variable templates.
- `.github/workflows/`: CI and release/deployment workflows.
- `docs/remediation/`: remediation roadmap, live status, and architecture decisions.
- `scripts/`: local development and deployment helpers.

Instructions in a deeper `AGENTS.md` override this file for that subtree. Preserve unrelated user changes and keep every remediation task independently reviewable.

## Safety boundaries

- Never read, modify, print, or copy repository `.env` files, API keys, tokens, or database passwords.
- Before the R0.1 database-test fuse is complete, do not run any backend test command that could connect to PostgreSQL.
- Database tests may read only `TEST_DATABASE_URL`; they must not call the production config loader or fall back to `DATABASE_URL` or `.env`.
- Database access in tests requires all of the following before connection creation or DDL: `ALLOW_DB_TESTS=1`, a database name ending in `_test`, and a host allowed by the test-host allowlist.
- If database-test safety cannot be established, skip the database validation and report why. Never fall back to another database.
- Do not run migrations, backfills, `CREATE EXTENSION`, `DROP SCHEMA`, `DELETE`, or `TRUNCATE` against a database of unknown provenance.
- Do not add production dependencies, run irreversible migrations, remove data or fields, or change public APIs/product semantics without pausing for user approval.
- Do not use `git reset --hard`, `git checkout --`, or commands that discard user work. Do not commit, push, or create a PR unless explicitly requested.

## Build, test, and lint commands

Run commands from the repository root unless a command changes directory explicitly.

### Backend

- Format changed Go files: `gofmt -w <files>`
- Run a known database-free package test: `go test ./apps/server/internal/config`
- Run all backend tests only after R0.1 is complete and the database-test contract is satisfied: `go test ./apps/server/...`
- Build server: `go build ./apps/server/cmd/server`

Do not assume a package is database-free merely because its name is not `postgres`; inspect its tests and helpers first.

### Frontend

From `apps/web/`:

- Install locked dependencies: `npm ci`
- Tests: `npm test -- --run`
- Lint: `npm run lint`
- Build: `npm run build`

### Containers

- Server image: `docker build -f apps/server/Dockerfile -t docreview-agent-server:ci .`
- Web image: `docker build -f apps/web/Dockerfile -t docreview-agent-web:ci apps/web`

## Database-test rules

Database integration tests must obtain their connection target through the shared test-support contract. The contract must fail before any connection factory, migration runner, schema creation, extension creation, or cleanup callback is invoked when configuration is unsafe. Unit tests for the fuse must use injected fakes and must never open a network connection.

The CI PostgreSQL database is `agent_project_test`. CI must explicitly set `ALLOW_DB_TESTS=1`, `TEST_DATABASE_URL`, and the approved test-host allowlist. Production code continues to use `DATABASE_URL`; test code must not.

## Task implementation flow

For each remediation task:

1. Investigate current code, governance files, and `git status`; confirm the root cause and affected files before editing.
2. Plan small verifiable steps, including failure-path tests, compatibility, and rollback.
3. Add failure-path coverage first where practical, then make the smallest sufficient implementation.
4. Run directly relevant safe tests plus format, lint, type checks, or builds; record commands and results.
5. Review the diff for security, tenancy, transactions, concurrency/idempotency, crash recovery, compatibility, test quality, and scope creep.
6. Update `docs/remediation/status.md`, then proceed only to the next task in the current phase.

Use expand → dual write/backfill → verify → switch read → contract for database changes. Use new contract → shadow/dual write → reconcile → switch → remove old path for architecture migrations.

## Definition of Done

A task is done only when:

- The root cause is confirmed against current code.
- Normal and failure paths are covered by tests.
- The minimal implementation is complete and compatibility is preserved.
- Relevant safe verification commands pass; every skipped command has a documented reason.
- The complete task diff has been self-reviewed and contains no unrelated changes.
- Rollback steps, residual risks, and the next task are recorded in `docs/remediation/status.md`.
- Every task-specific acceptance criterion in `docs/remediation/roadmap.md` is satisfied.

## Prohibited shortcuts

- Do not replace root-cause fixes with keyword matching, hardcoded special cases, duplicate state, swallowed errors, or silent fallback.
- Do not perform opportunistic refactors or modify code outside the active task.
- Do not expose the backend directly to the public internet without an explicit protected ingress boundary.
- Do not use wildcard CORS origins for the application API.
- Do not advance to the next remediation phase without the user's confirmation after the current phase gate report.
