# Production Agent Runtime Implementation Plan

This plan implements [ADR 0001](./decisions/0001-durable-typed-agent-runtime.md). Progress and verification evidence belong in `status.md`. A phase gate stops before the next phase unless the user explicitly authorizes continuation.

## Progress

- Phase A: complete; database-test isolation and connection-before-validation proof passed.
- Phase B: implemented; safe local tests and builds passed, database execution deliberately not performed.
- Phase C: implemented; safe local verification passed, guarded PostgreSQL execution skipped without an authorized test database.
- Phase D: implemented and gate passed; TurnCoordinator, atomic turn/outcome storage, shared persisted-event projection, ContextAssembler, and manifest reproduction passed safe local verification. PostgreSQL execution was deliberately skipped without an authorized test database.
- Phase E: implemented and gate passed; ToolRuntime, deterministic PolicyEngine, durable audit/approval/artifact/rate-limit stores, typed built-in adapters, and web provider controls passed safe local verification. PostgreSQL execution was deliberately skipped without an authorized test database.
- Phase F: gate passed by explicit user authorization.
- Phase G: implemented; Canonical AST, independent importer/renderer boundaries, strict node-ID PatchSet, deterministic Validator, canonical Tool backend, atomic idempotent version-bundle Committer, and expand-only migration 021 passed safe local verification. PostgreSQL execution was deliberately skipped without an authorized test database. Paused at the Phase G gate.
- Phase H: implemented; strict EvidenceSet 1.0, current/explicit-history scope, independently degradable lexical/semantic recall, versioned fusion/threshold/rerank, strict embedding compatibility, workspace-scoped PostgreSQL retrieval, migration 022 with HNSW, ToolRuntime/ContextAssembler integration, Artifact citation summaries, and retrieval-eval-v1 passed deterministic local verification. PostgreSQL execution was deliberately skipped without an authorized test database. Paused at the Phase H gate.
- Phase I: implementation gate passed and the user explicitly authorized Phase J. Authorized PostgreSQL/protected-ingress/shadow/canary evidence is still not present in this workspace and remains mandatory evidence before legacy contraction.
- Phase J: versioned offline evaluation, CI report gate, production operations/monitoring contracts, audited recovery migration 024, and a deterministic legacy-removal fuse are implemented. Legacy deletion is blocked because current production callers/configuration dependencies and live cohort evidence do not satisfy the removal fuse.

## Phase A — Safety baseline and test isolation

- Keep all PostgreSQL tests behind the shared process-environment-only fuse.
- Require `ALLOW_DB_TESTS=1`, `TEST_DATABASE_URL`, `_test` database suffix, and effective-host allowlist match.
- Prove unsafe configuration fails before connection factories, DDL, migrations, or cleanup registration.
- Keep CI on `agent_project_test`; never fall back to `DATABASE_URL` or dotenv.
- Safe verification: fuse/config unit tests, compile-only/full suite with DB variables removed, static bypass audit, build, diff review.
- Rollback: test-only helper/CI rollback; no schema or production state changes.

## Phase B — Target schema and durable storage contracts

- Add append-only migrations for runs, steps, attempts, tool calls, context manifests, outbox events, turns/request IDs, and required indexes/constraints.
- Add migration-ledger checksums and explicit offline migration ordering.
- Implement repositories and pure transition/claim contracts before a worker.
- Compatibility: expand-only schema; legacy tables and reads remain active.
- Rollback: disable writers; leave expanded tables inert.

## Phase C — Durable Run Engine

- Implement claim + lease + heartbeat with `SKIP LOCKED`, lease-generation checks, exponential backoff, retry/terminal error classes, timeout hierarchy, cancellation, approval waits, budget stops, and startup recovery.
- Make channel notification an optional wake-up only.
- Add crash/lease-expiry, late completion, retry, idempotency, cancellation, deadline, and restart tests.
- Compatibility: shadow-create runs from an allowlisted legacy task cohort without executing side effects.

## Phase D — TurnCoordinator and ContextAssembler

- Add atomic `turn_id`/`request_id` ingestion and response outcome commits.
- Make stream/non-stream adapters call one Turn Pipeline; SSE projects persisted events.
- Implement model-versioned token estimation, layer budgets, output reservation, artifact references, trust/provenance, and persisted ContextManifest.
- Add duplicate request, interrupted stream, large-document, budget priority, and manifest reproduction tests.

## Phase E — ToolRuntime and PolicyEngine

- Implement descriptors, registry/discovery/router, JSON Schema validation, permission/risk policy, approvals, timeouts/retries/rate limits, idempotency, audit, provenance, artifacts, and classified errors.
- Wrap document version/read/search, retrieval, artifact read/write, patch validate/commit, approval request, and policy-governed web search.
- Distinguish mock and production web providers; add privacy/domain/quota policy and injection tests.

## Phase F — Typed orchestration

- Implemented: typed node set, strict Decision schema, deterministic ActionValidator, and bounded Supervisor Plan–Act–Observe.
- Implemented: immutable manifest use for every model call, ToolRuntime-only actions, durable observations, trace propagation, approval continuation, and classified model/tool failures.
- Implemented: deterministic success/wait/no-progress stops; Engine retains budget/limit/deadline/timeout/cancellation/retry enforcement.
- Implemented: canonical output comparison persistence for later allowlisted legacy-vs-typed shadow reconciliation. No public route or duplicate write was enabled.
- Gate: safe local verification complete; authorized PostgreSQL execution remains required before deployment. See `typed-orchestration.md`.

## Phase G — Canonical Document AST, PatchSet, and atomic commit

- Implemented: stable nodes, source/page mapping, independent importer/renderer interfaces, canonical hashing, graph validation, and AST-derived section/chunk/embedding profiles.
- Implemented: strict node-ID PatchSets, expected hashes, trusted node authorization, categorized deterministic validation, and one version-bundle/idempotency/Outbox transaction.
- Implemented: DOCX/PDF/Markdown importer/renderer capability boundaries and structure/page/metadata preservation regressions.
- Implemented: expand-only migration 021 and an unwired canonical Tool backend. No public flow, read default, migration, or backfill was switched.
- Gate: safe local verification complete; authorized PostgreSQL execution and later dual-write/backfill/reconciliation remain required. See `canonical-document-runtime.md`.

## Phase H — Evidence and retrieval

- Implemented: strict versioned EvidenceSets with lexical/vector/fused scores, untrusted content, hashes, timestamps, and complete recall/filter/fusion/rerank/degradation provenance.
- Implemented: current-version default plus explicit `include_history`/`version_id`, with workspace/resource/version rechecks at Service and PostgreSQL boundaries.
- Implemented: independently degradable lexical/semantic recall, versioned weighted/RRF fusion, configurable threshold/rerank, and fail-closed embedding profile/model/dimension/vector compatibility.
- Implemented: expand-only migration 022, ready-vector profile metadata, retrieval profile facts, scoped indexes, and cosine HNSW.
- Implemented: `retrieval.search@2.0.0` through ToolRuntime/PolicyEngine, persisted tool-version fencing, Artifact citation summaries, Evidence ContextItems, and `retrieval-eval-v1` Recall/long-document/failure fixtures.
- Gate: safe local verification complete; authorized PostgreSQL/HNSW/query-plan execution remains required before deployment. See `evidence-retrieval.md`.

## Phase I — Vertical-slice cutover

- Implemented: exact trusted Workspace/Resource cohort routing through request → retrieval → analysis → PatchSet → validation → external approval → atomic commit → Outbox/public Turn projection.
- Implemented: one stream/non-stream Turn Pipeline, durable `request_id` replay, persisted SSE sequence recovery, runtime/projection lease recovery, approval continuation, and deterministic frontend waiting/terminal states.
- Implemented: read-only shadow EvidenceSet evaluation and persisted result/event/public-DTO hash comparison without duplicate Turn, Run, tool, approval, Patch, or commit writes.
- Implemented: immediate new-request rollback to legacy while immutable durable Runs continue to drain under workers restricted to persisted `runtime_mode='durable'` facts.
- Gate: local deterministic verification complete without a database connection. Authorized migration 023/round trips, protected-ingress validation, reviewed shadow reconciliation, and an explicit one-cohort durable canary remain mandatory. See `agent-runtime-cutover.md`.

## Phase J — Evaluation, operations, and legacy removal

- Implemented: strict `agent-runtime-eval-v1` inputs/gold/candidate/report contracts and scorers for goal understanding, recall, citations, node targeting, Patch fidelity, unauthorized-change rate, prompt injection, long documents, lexical/semantic/Web degradation, crash recovery, request/tool/commit idempotency, approval consistency, and Workspace isolation.
- Implemented: CI report artifact and exit-code gate; every case carries Run/Step/Attempt/Tool/ContextManifest/EvidenceSet identities.
- Implemented: Workspace-scoped Run/Step/Attempt/Tool/ContextManifest/Approval/Outbox diagnostics, trace index, queue/lease/error/token/cost/retrieval/reconciliation metrics, and audited cancel/retry/projection replay operations. Approval remains on the trusted owner/admin API.
- Implemented: append-only migration 024 and deployment, recovery, replay, cancellation, monitoring, incident, degradation, and rollback runbooks.
- Implemented: deterministic removal criteria for shadow/durable cohorts, database/ingress/canary evidence, compatibility, all acceptance scenarios, and zero production callers/configuration dependencies.
- Deferred by the removal fuse: deleting ADR 0001 legacy abstractions. Current `cmd/server/main.go`, configuration, Compose, README, task/job/approval paths, and legacy document projections remain production dependencies; live cohort/database evidence is absent locally.

## Cross-phase invariants

- LLM output never authorizes an operation or directly commits data.
- Every model input goes through ContextAssembler; every tool call goes through ToolRuntime.
- Messages and runtime events are facts; summaries and projections are rebuildable.
- Every long-running or writing operation is persistent, resumable, retryable, cancellable, audited, and idempotent.
- No real migration, backfill, destructive DDL, new production dependency, or public semantic switch occurs without the repository safety gate and required user approval.
