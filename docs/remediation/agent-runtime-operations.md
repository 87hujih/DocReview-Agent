# Agent Runtime Production Operations

## Boundary

`internal/agent/operations` is the Workspace-scoped operator contract. `storage/postgres/agentops` supplies read-only diagnostics/metrics and audited mutations. `cmd/agent-runtime-ops` ships in the server image, reads only the explicit process `DATABASE_URL`, never loads dotenv files, and never runs migrations.

Migration `024_agent_runtime_operations.sql` is append/expand-only and adds only `agent_runtime_operator_actions`. Each mutating request requires a Workspace, stable request ID, operator ID, reason, target, and timestamp. `(workspace_id, request_id)` is the idempotency key. The action and state transition commit together.

The frontend reads bounded, exact-Workspace operational projections from `GET /api/agent/runs`, `GET /api/agent/runs/:id`, `GET /api/agent/approvals`, and `GET /api/agent/approvals/:id`. Public Run detail deliberately excludes raw Run state, Step/tool input and output, ContextManifests, Attempts, Outbox payloads, and internal trace indexes. Typed approval decisions remain on `POST /api/agent/approvals/:id/approve|reject`. They require the signed trusted-ingress identity and an active owner/admin membership; the decision remains atomic with the exact approved `CommitPatch` continuation or rejected terminal state. There is intentionally no unauthenticated database approval shortcut in the operations CLI.

## Inspection and monitoring

Examples run inside the protected server container so no database credential is printed:

```text
docker compose -f deploy/docker-compose.prod.yml exec -T server \
  ./agent-runtime-ops -action diagnose -workspace-id <workspace-uuid> -run-id <run-uuid>

docker compose -f deploy/docker-compose.prod.yml exec -T server \
  ./agent-runtime-ops -action metrics -workspace-id <workspace-uuid> \
  -resource-id <resource-uuid> -window 1h

docker compose -f deploy/docker-compose.prod.yml exec -T server \
  ./agent-runtime-ops -action comparisons -workspace-id <workspace-uuid> \
  -resource-id <resource-uuid> -window 720h -limit 200
```

Diagnostics return the scoped Run, ordered Steps, Attempts, Tool calls, ContextManifests, approvals, related Outbox events, findings, and a trace index joining run/step/attempt/tool/manifest/EvidenceSet IDs. Cross-Workspace targets return not found.

`comparisons` is a read-only review export. It requires an exact Workspace/Resource, enforces a 1–1000 row limit and at most a 30-day window, and returns immutable comparison ID, request/Run identity, status, hashes, details, and creation time in stable order. It does not mark a row reviewed and does not set any legacy-removal evidence flag; the human review record must retain the exported identity and its external reviewer/audit trail.

Metrics schema `1.1` accepts an optional exact Resource scope. A Workspace-only snapshot remains useful for general monitoring, but it is not sufficient for legacy removal. For an exact Resource, Run status counts contain only `runtime_mode='durable'` Runs created in the selected window; current Step/approval/Outbox drain state remains scoped to all durable Runs for that Resource. The snapshot also contains:

- Run/Step status counts, queued/running Steps, oldest queued age, expired leases, pending approval count/age;
- Attempt count/error rate, input/output tokens, and recorded cost for the selected window;
- Outbox pending/publishing/dead-letter counts, expired leases, and oldest pending age;
- retrieval calls/success, lexical/semantic/Web degradation, profile mismatch, missing node citation count, and mean Evidence count;
- shadow comparison matched/diverged/unavailable counts.

Retrieval profile mismatch is counted from the stable `details.reason_code=embedding_profile_mismatch`. A historical retrieval failure without a structured reason code or error category is counted conservatively in the same blocking metric, so older localized message text can never create a false zero. Use a post-deployment window with fully classified records or investigate every unclassified failure before setting `retrieval_metrics_verified=true`.

Neither `metrics` nor `comparisons` is sufficient by itself to satisfy the deletion fuse. Operators must reconcile at least 100 exported comparison facts, diagnose the referenced Runs/traces where applicable, retain reviewer evidence, and only then update the reviewed cohort fields. Empty or zero output without the exact cohort, a complete window, and a retained audit is not proof.

### Offline shadow review bundle

Create the review manifest from the exact exported bytes on a controlled operator workstation:

```text
go run ./apps/server/cmd/agent-shadow-review \
  -action template \
  -comparisons <comparisons-export.json> \
  > <shadow-review.json>
```

The template binds the raw export SHA-256 and copies every comparison ID, request/Run identity, status, and legacy/typed result/event/DTO hash. Fill a stable `review_id`, externally authenticated `reviewer_id`, `reviewed_at`, and each `decision`. Use `confirmed` only after checking the retained response/trace facts. `diverged`, `unavailable`, or `disputed` entries require review notes.

Verify without modifying either input:

```text
go run ./apps/server/cmd/agent-shadow-review \
  -action verify \
  -comparisons <comparisons-export.json> \
  -review <shadow-review.json> \
  > <shadow-review-report.json>
```

Exit `0` means the review completely covers an untruncated export and its identities/hashes still match; it only authorizes transcription of the reported shadow counts into a separately reviewed evidence bundle. Exit `1` means the JSON is valid but incomplete, disputed, missing required notes/identity, or may be truncated. Exit `2` means the schema, cohort, digest, ID, status, hash, time window, or JSON is invalid. An export returning exactly its requested limit is conservatively treated as possibly truncated; rerun with a higher limit or narrower window. The reviewer string is audit metadata, not authentication, so the surrounding platform must retain the authenticated reviewer/change record.

This command does not consume metrics, set `projection_metrics_verified`/`retrieval_metrics_verified`, update `legacy-removal-evidence.current.json`, or authorize deletion. Metrics, durable canary, database, ingress, compatibility, rollback, public behavior, security, caller, and configuration evidence remain separate gates.

Recommended initial alerts are: any projection dead letter or retrieval profile mismatch; any expired lease older than one recovery interval; oldest queued or Outbox pending age above the Run SLO; attempt error rate above 5% for 15 minutes; shadow unavailable above 0%; unexplained shadow divergence above 1%; or token/cost slope above the configured cohort budget. Tune only from reviewed staging/canary evidence.

## Safe actions

All examples require a unique operator request ID and incident/change reason:

```text
./agent-runtime-ops -action cancel -workspace-id <workspace> -run-id <run> \
  -request-id <stable-id> -operator-id <operator> -reason "INC-123 cancellation"

./agent-runtime-ops -action retry -workspace-id <workspace> -run-id <run> \
  -request-id <stable-id> -operator-id <operator> -reason "INC-123 transient provider recovered"

./agent-runtime-ops -action replay-dead-letter -workspace-id <workspace> -event-id <event> \
  -request-id <stable-id> -operator-id <operator> -reason "INC-123 projector fixed"
```

- Cancel sets the durable cancellation request and wakes a waiting Step. The Worker performs the fenced terminal transition.
- Retry accepts only a failed Run whose latest failed Step has `rate_limited`, `timeout`, `retryable_upstream`, or `lease_expired`. It requeues the same Step, retains `attempt_count`, permits one bounded next attempt, and therefore reuses existing tool/commit idempotency identities.
- Dead-letter replay accepts only `agent.step.outcome_committed` and `agent.tool_approval.rejected`. It requeues the same Outbox event ID and immutable payload; projectors use Event ID as their idempotency key. Commit/write events are never replayed by this operation.

Never invent a new request/tool/commit/event key, edit approval rows, rewrite Run status manually, delete audit facts, reset leases, or replay arbitrary Outbox types.

## Deployment, migration, and rollback

1. Back up the database and block production Agent traffic while validating migrations 021–024 on an authorized `_test`/staging database.
2. Run the checksum migrator once; do not edit migrations 016–024 after application.
3. Deploy the image containing `server`, `migrate`, and `agent-runtime-ops`.
4. Run scoped diagnostics and metrics read-only. Confirm no unexpected durable backlog, expired lease, dead letter, or profile mismatch.
5. Validate trusted ingress, Workspace/canonical data reconciliation, durable canaries, approvals, reconnect, retry, cancel, and safe projection replay.

The current application does not provide a configuration-level legacy rollback. Remove new Agent traffic at ingress, leave Workers draining existing durable Runs, then deploy the previous verified release. Keep migration 024 and its audit rows; older servers ignore the additive table. Stop using the operations binary if its code is rolled back. Never reverse a rollback by deleting Run/Tool/Outbox/operator history.

## Incident sequence

1. Capture `diagnose` and `metrics` JSON before mutation.
2. Classify the failure: lease expiry, retryable provider error, approval wait, deterministic conflict, projection dead letter, profile mismatch, or tenant/policy denial.
3. Contain new traffic at the trusted ingress; do not stop Workers draining accepted durable Runs and do not set `AGENT_RUNTIME_MODE=legacy` because the current build rejects it.
4. Use cancel/retry/replay only when its preconditions match. Use the signed approval API for approval decisions.
5. Re-run diagnostics with the same target and retain the operator action ID in the incident record.
6. Reconcile public Turn projection, canonical version/commit, Outbox receipt, and durable metrics before restoring traffic.

Provider/Web degradation may continue only through the declared fallback matrix. Embedding profile mismatch, authorization failure, unknown tool output, Patch conflict, cross-Workspace scope, or missing approval fails closed and is not an operator-retry justification.
