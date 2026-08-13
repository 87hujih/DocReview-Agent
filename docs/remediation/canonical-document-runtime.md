# Phase G Canonical Document Runtime

Phase G adds an inert strangler path for Canonical Document AST import, node-ID PatchSets, deterministic validation, and atomic version-bundle commit. Public APIs, legacy reads, uploaded-document ingestion, the legacy approval worker, and default traffic remain unchanged.

## Module boundaries

- `internal/document/model`: format-neutral AST, stable identity, canonical hashing, graph validation, and AST-derived projections.
- `internal/document/importer`: independent Markdown, DOCX-extracted-text, and PDF-extracted-text importers.
- `internal/document/renderer`: independent Markdown, DOCX compatibility, and PDF compatibility renderer boundaries.
- `internal/document/patch`: strict PatchSet decoding, limits, hashing, and in-memory application.
- `internal/document/validation`: trusted-snapshot conflict, authorization, reference, structure, metadata, and page validation.
- `internal/document/commit`: validation-before-write, idempotency replay, canonical tool adapter, and atomic store contract.
- `internal/storage/postgres/documentcommit`: PostgreSQL lock/recheck and one-transaction version-bundle persistence.

Patch generation remains a semantic orchestration responsibility. Validation and commit are separate deterministic responsibilities. No model output, Agent, Handler, or legacy executor receives a database transaction or repository write primitive.

## Canonical AST 1.0

Each document contains:

```json
{
  "document_id": "resource UUID",
  "version_id": "resource version UUID",
  "root": {},
  "source_format": "markdown | docx | pdf",
  "metadata": {},
  "content_hash": "sha256:...",
  "schema_version": "1.0"
}
```

Each node contains `node_id`, `type`, `attributes`, `content`, ordered `children`, `source_location`, zero or more `page_mapping` entries, `metadata`, and `content_hash`. The root must be a `document` node. Node IDs are stored as text and are unique within a version; the same stable ID may occur in successive versions.

Importer IDs use SHA-256 over the document identity, structural source path, and node type, not editable content. Re-importing the same source structure yields the same IDs and distinct source positions cannot collide under the deterministic path contract. Patch application preserves existing IDs and requires explicit non-conflicting IDs for inserted nodes. A structurally reordered fresh import may intentionally allocate new IDs; a future binary extractor upgrade must therefore use an explicit importer/extractor profile and reconciliation before backfill.

Node hashes cover type, attributes, content, ordered child IDs, source location, page mappings, and metadata. The document hash covers the complete AST envelope. Hashes use canonical Go JSON encoding with deterministic map-key ordering and are recalculated after a Patch.

Graph validation rejects missing IDs/types, duplicate IDs, cycles, multiply-parented nodes, invalid source offsets, invalid/non-positive pages, non-finite or non-JSON metadata, stale node hashes, and stale document hashes. Nested nodes are the ownership graph; PostgreSQL additionally enforces deferred `(version_id, parent_node_id)` references so flattened rows cannot become orphans.

Markdown is parsed into heading and paragraph nodes. DOCX and PDF importers consume UTF-8 text already extracted by a trusted format adapter; PDF form-feed boundaries become page nodes and page mappings. Raw binary parsing and binary packaging are delivery adapters, not AST semantics. This prevents Tika/plain-text extraction from becoming the canonical data model while retaining it as an upstream compatibility option.

Sections, chunks, page ranges, resource metadata, chunk profile, embedding profile, and deterministic `pending` embedding status are derived only from AST nodes. Embeddings are not generated inside the commit transaction. The committed Outbox event is the durable request for asynchronous projection/embedding work.

## PatchSet 1.0 and limits

The accepted envelope is:

```json
{
  "schema_version": "1.0",
  "resource_id": "...",
  "base_version_id": "...",
  "operations": [
    {
      "op": "replace_node",
      "node_id": "...",
      "expected_hash": "sha256:...",
      "content": "..."
    }
  ],
  "evidence_refs": [],
  "reason": "..."
}
```

Supported operations are `replace_node`, `insert_before`, `insert_after`, `delete_node`, and `update_attributes`.

- Every operation targets a stable existing `node_id` and supplies its lowercase SHA-256 `expected_hash`.
- Insertions additionally supply a complete new node plus `expected_parent_id` and `expected_parent_hash`.
- New IDs must not collide with current or earlier inserted IDs.
- The root cannot be deleted. Multiple operations cannot target the same base node, and a later operation cannot target a deleted node.
- `update_attributes` merges explicit keys; a JSON null removes a key.
- Unknown fields, duplicate keys at any depth, trailing JSON values, unsupported operation payload combinations, duplicate/blank evidence references, and unsupported schema versions are rejected.
- Defaults are 256 KiB, 100 operations, 100 evidence references, and JSON depth 24. Tool input schema also caps operations/evidence before backend execution.

The canonical Patch hash is persisted with the commit idempotency fact. Model output can propose only this envelope; it cannot supply workspace identity, principal identity, node authorization, approval, current version, current hashes, or a transaction.

## Deterministic Validator

The Validator receives a trusted snapshot containing workspace, resource, current version, Canonical AST, allowed node IDs, and existing evidence references. It checks, in deterministic order:

1. Patch schema and operation contract.
2. policy-blocked state and workspace/resource scope.
3. stored AST validity and current `base_version_id`.
4. evidence-reference completeness.
5. target existence and node-level authorization.
6. target and insertion-parent expected hashes.
7. duplicate/new node IDs and legal operation order.
8. required root structure, parent references, cycles/orphans, metadata, source ranges, and page mappings after application.

Violations use only these categories: `invalid_patch`, `version_conflict`, `hash_conflict`, `unauthorized_node`, `reference_missing`, `structural_conflict`, `resource_scope_denied`, and `policy_blocked`.

Untrusted document/evidence text is never consulted for authorization. The canonical Tool backend resolves requested target/parent IDs through a trusted `NodeAuthorizer`, then passes that immutable set to the Validator. Prompt injection therefore remains data.

## Atomic Committer transaction

The Committer first hashes the Patch and checks the workspace-scoped idempotency fact. An identical retry returns the recorded version/outbox IDs without revalidation or a new write. A different Patch under the same key returns an idempotency conflict.

The storage request carries an unexported Committer proof checked by the PostgreSQL adapter, so a direct Agent/Handler/repository caller cannot construct an accepted atomic request. Serialization and deadlock errors retain a retryable category for the durable Engine; semantic version/hash/idempotency conflicts remain terminal conflicts.

For a new commit, the Committer loads the trusted snapshot, validates, applies the Patch, allocates the new version ID, recalculates hashes, derives projections, and renders only a compatibility `resource_versions.content` value. The PostgreSQL store then starts one Serializable transaction and performs:

1. a workspace/idempotency advisory transaction lock and a second idempotency lookup;
2. workspace-scoped resource and current-version row locks;
3. a second `base_version_id` check;
4. locked rechecks of every target and insertion-parent hash;
5. insertion of `resource_versions` and its AST/schema/renderer/embedding profiles;
6. insertion of `canonical_documents`, `document_nodes`, and source/page mappings;
7. insertion of AST-derived `resource_sections` and `resource_chunks` with deterministic pending embedding metadata;
8. merge of canonical resource metadata;
9. insertion of one `document.version.committed` Outbox event;
10. insertion of `document_patch_commits` binding workspace/key, Patch hash/JSON, base/new versions, actor, and Outbox ID;
11. transaction commit.

Any failure rolls back every fact. A version cannot exist without its AST, nodes, derived projections, idempotency fact, and Outbox event. Deferred root/parent/canonical-node foreign keys are checked at commit. Embedding generation occurs only after the pending fact is durable; it cannot produce a half-complete version.

`patch.commit` accepts only `commit.CanonicalToolBackend`, whose unexported marker can be implemented only inside the `commit` package. That adapter always performs trusted node authorization and calls the Validator/Committer. ToolRuntime still applies descriptor schema, workspace resource PolicyEngine checks, bound external approval, rate limit, idempotency audit, and output validation before the adapter runs.

## Migration 021

`021_canonical_document_ast.sql` is append-only and expand-only. It adds:

- canonical document/version profile and AST storage;
- stable document nodes with deferred parent references;
- source and page mappings;
- Section/Chunk canonical-node and profile fields;
- resource metadata;
- workspace-scoped Patch commit idempotency facts linked to versions and Outbox;
- uniqueness, checks, foreign keys, and lookup indexes.

No migration, DDL, backfill, or live database connection was executed locally. Live tests remain behind `ALLOW_DB_TESTS=1`, process-only `TEST_DATABASE_URL` ending in `_test`, and the explicit effective-host allowlist.

## Deployment, compatibility, and rollback

Deployment order:

1. deploy migration 021 with the checksum migrator while retaining `AGENT_RUNTIME_MODE=legacy`;
2. deploy the unwired AST/Patch/Validator/Committer code;
3. run guarded PostgreSQL transaction tests and import/render fixtures on an authorized test or staging database;
4. later, dual-write/backfill an explicitly selected cohort and reconcile AST-derived projections against legacy rows;
5. do not switch reads, public APIs, or durable production traffic before later phase gates.

Rollback before migration execution removes migration 021 and the new unwired packages from the candidate build. Rollback after execution disables/stops canonical writers and retains additive AST, commit, and Outbox facts. Keep `legacy` routing and legacy reads; do not drop tables, delete commit history, or replay non-idempotent writes. An already committed canonical version remains auditable and compatible through its rendered `resource_versions.content` value.

The old Markdown/title-string path remains the Strangler rollback path and is not called by the new Runtime. Its deletion checklist, deferred until post-cutover approval, is:

- `internal/agent/executor` heading/body replacement and `applyPreparedRevisions` helpers;
- `task/workflow/editor_scope` heading/sub-string selection and diff mapping;
- `knowledge/sections.ParseMarkdown` as an edit-target identity mechanism;
- legacy `diff_preview` approval payloads and execution jobs as the write protocol;
- direct `resource_versions.content` plus chunk-only commit path;
- Tika/plain text structure as the default canonical representation;
- legacy Section/Chunk rebuild paths not derived from Canonical AST.

## Residual risks and Phase H boundary

- Migration 021 and repository SQL have not run against PostgreSQL in this workspace; CI or an authorized `_test` database must prove deferred constraints and transactional round trips.
- DOCX/PDF binary extraction/rendering profiles need production adapters and fixture reconciliation before import backfill; Phase G establishes the contract, not a public binary cutover.
- Positional importer IDs are stable for identical source structure and Patch-preserved across versions, but a fresh structurally reordered import needs reconciliation rather than blind identity reuse.
- Legacy and canonical projections can drift until dual-write/backfill reconciliation is explicitly authorized.
- Phase H must add versioned EvidenceSets and retrieval/profile compatibility. Phase G does not switch retrieval defaults or public traffic.
