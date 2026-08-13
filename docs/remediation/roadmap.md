# DocReview Agent Remediation Roadmap

The project will evolve through small, independently verifiable tasks. Complete all tasks in a phase, run its gate, report, and pause for user confirmation before entering the next phase.

## Phase 0 — Risk containment

Goal: isolate test databases, close production network exposure, enforce an explicit CORS allowlist including `PATCH`, run frontend tests in CI, and track remediation governance.

### R0.1 — Test database fuse

Done when test code no longer calls the production config loader; database tests read only `TEST_DATABASE_URL`; `ALLOW_DB_TESTS=1` is mandatory; the database name ends in `_test`; the host passes an explicit test allowlist; unsafe configuration fails before connection or DDL; CI uses `agent_project_test`; and fuse unit tests open no database connection. No database integration test may run before this task is complete.

### R0.2 — Network and CORS containment

Done when wildcard CORS is removed; allowed origins come from an explicit allowlist; `PATCH` preflight succeeds; production fails closed without an allowed origin; protected ingress prevents an unguarded direct public backend port; explicit local-development configuration remains supported; and configuration/CORS tests cover success and failure paths.

### R0.3 — CI and documentation governance

Done when CI runs `npm test -- --run`, lint, and build; `AGENTS.md` and `docs/remediation/` are trackable; README CI documentation matches the workflow; and the CI diff passes self-review.

Phase gate: run all safety-permitted tests, inspect the complete diff, validate every Phase 0 criterion, update status, report, and stop before Phase 1.

## Phase 1 — Data and identity foundations

Create a migration ledger with checksums; move migrations out of application startup; introduce User, Organization, Workspace, and Membership; add Principal, authentication middleware, and WorkspaceScope; enforce ACLs, quotas, rate limits, and principal audit; and test cross-tenant isolation.

### R1.1 — Migration ledger and offline migration lifecycle

Add a full-filename migration ledger with SHA-256 checksums, reject checksum drift before applying pending work, and make each migration plus its ledger record atomic. Provide an explicit migration command and deployment ordering, and remove migration execution from server startup. Unit tests must not connect to a database; integration tests run only under the R0.1 fuse.

### R1.2 — Identity and tenancy schema expand

Add User, Organization, Workspace, Membership, and principal-audit storage contracts. Add only nullable workspace/principal scope columns and supporting indexes to existing roots. Do not backfill, switch reads, add `NOT NULL`, remove fields, or execute a real migration in this task.

### R1.3 — Principal, authentication adapter, and WorkspaceScope

Define typed Principal and WorkspaceScope contracts and an authentication adapter with a compatibility/shadow rollout. Identity-provider and trusted-ingress selection is a mandatory approval point before public API authentication semantics change.

### R1.4 — ACL, quota, rate-limit, and principal audit policy

Introduce deterministic policy checks with observe/shadow before enforcement, including denial, exhaustion, throttling, and audit failure-path tests. Enforcement requires the R1.3 identity decision and an explicit reversible configuration boundary.

### R1.5 — Dual write, reconciliation, scoped reads, and tenant gate

Dual-write identity/scope, provide dry-run backfill and reconciliation, switch reads only after verification, and add cross-tenant isolation tests. Do not execute backfill or contract/drop old paths without the required approval and safe test database.

Phase gate: verify migration checksums and deployment ordering; verify identity, scope, policy, audit, and cross-tenant tests; inspect the complete diff; update status; and stop before Phase 2.

## Phase 2 — Canonical document source of truth

Introduce a canonical document AST, stable node IDs, and `DocumentVersionBundle`; atomically commit sections, chunks, embeddings, and structure; replace Markdown-heading string edits with node patches; backfill and reconcile historical versions through dual write; and state PDF/DOCX output capability boundaries.

## Phase 3 — Durable workflows

Persist workflow run/step/attempt; implement database claim, lease, and heartbeat; add checkpoints, retries, backoff, stale-running recovery, idempotency keys, outbox, dead-letter handling, manual replay, three-level timeout budgets, and crash-injection tests.

## Phase 4 — Turn consistency and assistant service decomposition

Add `turn_id`/`request_id`; atomically create user messages and turns; atomically commit assistant outcomes; move projections and side effects to an outbox; recover interrupted streams; share one Turn Pipeline between streaming and non-streaming paths; and gradually extract TurnCoordinator, ContextAssembler, DecisionEngine, OutcomeRenderer, ConversationStore, ProjectionWorker, and WorkflowGateway.

## Phase 5 — Typed Agent Runtime

Implement Understand → Retrieve → `Finding[]` → `Patch[]` → Validate → Approval → Commit; remove the reviewer's plain-text bottleneck; add Model Gateway and Prompt/Schema Registry; record provider/model/token/cost/latency/retry/trace data; use typed evidence, provenance, and trust levels; defend against prompt injection; and require deterministic policy authorization for high-impact actions.

## Phase 6 — Production-grade RAG

Bind embedding profiles to dimensions; version indexes; filter by workspace/resource/current version; add HNSW or IVFFlat; fuse lexical and semantic retrieval; degrade safely when embedding or reranking fails; and test retrieval quality and latency.

## Phase 7 — Evaluation and production gates

Create versioned evaluation datasets and gates for citation correctness, node targeting, edit fidelity, prompt injection, long documents, interrupted streaming, retry, crash recovery, cross-tenant isolation, and capacity; establish staging, shadow, and canary rollout workflows.

## Change protocols

- Database: expand → dual write/backfill → verify → switch read → contract.
- Architecture: new contract → shadow/dual write → reconcile → switch → remove old path.
- Pause before production dependencies/external services, real migrations/backfills, destructive schema/data changes, public API or product-semantic changes, infrastructure-provider choices, unsafe verification, changes spanning more than three independent business domains, conflicts with user work, or changes that cannot be safely rolled back.
