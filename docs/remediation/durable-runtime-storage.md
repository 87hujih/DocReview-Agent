# Durable Agent Runtime Storage Contract

This document describes the expand-only storage foundation introduced by `017_durable_agent_runtime.sql`. Phase C can write inert allowlisted shadow facts; durable execution remains fail-closed until the typed graph is available. See [Durable Run Engine Operations Contract](./durable-runtime-engine.md).

## Durable facts

| Table | Fact owned | Primary safety constraints |
| --- | --- | --- |
| `agent_runs` | Supervisor objective, lifecycle, budgets, deadline, cancellation request, current step | explicit status enum, positive budgets, optimistic `version`, workspace/request uniqueness |
| `agent_steps` | Typed node input/output/error and execution ownership | `(run_id, step_key)` uniqueness, claim owner, lease expiry, lease generation, attempts/retry time |
| `agent_attempts` | One model/execution attempt and usage telemetry | `(step_id, attempt_number)` uniqueness, non-negative tokens/cost/latency |
| `tool_calls` | Audited typed tool invocation | JSON object inputs/outputs/errors and `(run_id, idempotency_key)` uniqueness |
| `context_manifests` | Exact context selection metadata seen by a model call | array items, tokenizer/hash, total plus reserved output within budget |
| `outbox_events` | Transactional publication/projection intent | aggregate idempotency, claim lease/generation, retry/dead-letter lifecycle |

All foreign keys from runtime facts either cascade only within the runtime aggregate or retain audit facts while optional legacy/workspace parents are removed. Existing product tables are not altered by this migration.

## Claim and lease contract

Step and outbox claims use a single database statement with `FOR UPDATE ... SKIP LOCKED`. A successful claim sets owner, expiry, heartbeat/attempt data, and increments `lease_generation`.

Heartbeat, completion, retry, and publication require all of:

- current running/publishing status;
- exact owner;
- exact lease generation;
- an unexpired lease.

This prevents a worker that resumes after expiry from overwriting a newer claimant. Startup recovery converts expired running steps back to queued when attempts remain, otherwise to failed; expired outbox publications return to pending. Process-local channels may later wake claimers but cannot create runnable truth.

## Idempotency contract

- Run creation: unique request ID within workspace; legacy/null-workspace requests use a separate global partial unique index.
- Step creation: stable `(run_id, step_key)`; changed type, input, or retry policy is a conflict.
- Tool calls: stable `(run_id, idempotency_key)`; changed step/tool/version/input is a conflict.
- Outbox events: stable aggregate plus idempotency key; changed event type/payload is a conflict.

Application code treats an identical replay as success and a different replay as `ErrIdempotencyConflict`. Database uniqueness remains the final concurrency guard.

## Transaction boundaries

The outbox repository accepts an existing `pgx.Tx`. Phase C uses it for lease-fenced step outcome/retry plus outbox commits. The document Atomic Committer remains a later phase.

## Deployment

1. Build an image containing migration `017_durable_agent_runtime.sql`.
2. On an explicitly authorized environment, run the existing one-shot migrator before application instances.
3. Verify the migration ledger checksum and the six tables/indexes/constraints.
4. Deploy application code in the default `legacy` mode.
5. Optionally set `AGENT_RUNTIME_MODE=shadow` with an explicit `AGENT_RUNTIME_SHADOW_RESOURCE_IDS` cohort. Shadow runs remain inert.

No backfill is required. No old table, field, API, task state, or worker path is switched.

## Rollback

Before migration execution, remove the new migration and storage packages from the candidate build. After execution, disable/omit durable workers and leave the additive tables in place; do not drop runtime audit data during an emergency rollback. A later destructive contraction requires a new reviewed migration and retention plan.

## Verification boundary

Pure tests validate lifecycle transitions, retry classification, migration structure, JSON shapes, idempotency input checks, `SKIP LOCKED`, lease-generation fencing, expiry fencing, and optimistic run transitions. Integration tests cover run/step/manifest/tool and outbox round trips, but run only through the repository database-test fuse. They are skipped when an explicitly authorized `_test` database is absent.
