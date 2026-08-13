# ADR 0002 — Evaluation, Operations, and Evidence-Gated Legacy Removal

- Status: Accepted
- Date: 2026-08-10
- Owners: Agent Runtime / Production Operations

## Context

ADR 0001 defines durable execution and a Strangler rollout but intentionally defers offline quality gates, a unified operator boundary, and legacy contraction. Phase I local evidence does not by itself prove a production cohort, PostgreSQL behavior, protected ingress, or absence of legacy callers.

## Decision

Adopt three Phase J contracts:

1. A strict versioned offline dataset/candidate/scorer/report interface that requires full Runtime trace identity and runs without a paid model.
2. A Workspace-scoped operations service and CLI for complete diagnostics, metrics, audited cancellation, bounded retry, and projection-only dead-letter replay. Human approval stays on the trusted owner/admin API.
3. A deterministic legacy-removal gate requiring cohort thresholds, acceptance evidence, data/rollback/public compatibility, and zero remaining production callers/config dependencies.

Add forward-only migration 024 for operator action audit facts. Do not change prior migrations. A repeated operator request returns the same action only when actor, reason, target, and action match; otherwise it conflicts.

## Safety and compatibility

Retry requeues the same failed Step and preserves attempt history and downstream idempotency identities. Dead-letter replay keeps the original Outbox Event ID and is allowlisted to idempotent public projections. Approval and commit identities are never created by operators. All reads and writes require exact Workspace scope.

The offline reference candidate measures deterministic contracts, not paid-provider quality. Representative corpora, latency/capacity, live query plans, and provider behavior remain staging/canary evidence.

Legacy deletion is not authorized merely because Phase J code exists. The gate must be eligible from recorded evidence. In the current repository, production callers and `legacy` configuration dependencies remain, so deletion is explicitly deferred.

## Rollout and rollback

Run evaluation in CI and retain the report artifact. Apply migration 024 only through the checksum migrator after authorized database validation. Ship the operations binary in the server image and invoke it only inside the protected operations boundary.

Rollback removes the candidate application/CLI wiring while retaining additive schema and audit facts. New traffic may route to `legacy`; already accepted durable Runs continue to drain. Never delete evaluation failures, Runtime history, Outbox facts, or operator action records to manufacture a passing gate.
