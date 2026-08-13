# Context Assembly Contract

## Boundary

Every typed model node must call `internal/agent/context.Assembler`. The assembler accepts already-authorized candidates and never queries a database, retrieval provider, or artifact store. This keeps selection deterministic and prevents prompt builders from acquiring hidden data access.

The output is an ordered `ContextManifest`, not an unconstrained prompt string. Model adapters serialize that structure with explicit data boundaries. The persisted manifest is the audit record of what the model actually saw.

## Layers and priority

Selection order is:

1. control;
2. task;
3. working memory;
4. evidence;
5. conversation memory;
6. artifact references.

The global input allowance is `token_budget - reserved_output_tokens`. Each layer may have a smaller budget. Control and task items are never silently truncated or removed; failure to fit returns `ErrRequiredContextBudget`. Evidence and conversation candidates are sorted by relevance and low-value items are dropped first.

Artifact-reference items never inline their supplied body. The model receives only the immutable reference, and a typed tool must read bounded content later. Full large documents therefore do not enter a prompt by default.

## Provenance and trust

Each selected item records layer/type, source/resource/version/node IDs, trust level, relevance, token count, content hash, selection reason, truncation flag, content/reference, and source creation time. Control context must have system trust. Evidence, conversation, and artifacts cannot claim system trust. Document, web, and tool-derived content remains explicit untrusted data even if it contains apparent administrator or system instructions.

## Token counting

`Tokenizer` is an injected versioned interface. `ModelEstimator` is the dependency-free conservative profile supplied in Phase D: CJK characters and punctuation count individually and alphanumeric runs use a four-byte estimate. The profile name and counts are persisted. An exact provider/model tokenizer can replace it without changing selection or storage contracts. Provider-reported usage remains billing truth.

## Persistence and reproduction

The PostgreSQL adapter writes the exact ordered item array, budget, reserved output, tokenizer profile, total count, and SHA-256 manifest hash to `context_manifests`. `GetContextManifest` returns the immutable stored record by ID and never re-runs retrieval or reads a newer document version.

## Failure behavior

Invalid layers/trust, non-object provenance envelopes, missing artifact references, negative relevance/counts, required-context overflow, or persistence failure abort the model call. There is no fallback to rune/character truncation and no fallback that inserts a complete document.
