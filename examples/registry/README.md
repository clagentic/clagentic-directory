# examples/registry

Example agent registry entries for clagentic-directory.

These files demonstrate valid agent capability entries for the registry.
They are served by the quick-start invocation in the top-level `README.md`.

## Schema version

All entries in this directory use `schema_version: 2`. New entries **must** use v2.

Schema v1 is accepted by the loader during a transition window but emits a deprecation
warning at load time. See [docs/SCHEMA.md](../../docs/SCHEMA.md) for the v1 reference and
[schemas/agent-entry.v2.yaml](../../schemas/agent-entry.v2.yaml) for the v2 schema.

## Vocabulary

Every intent, conversation_kind, and trust_label must come from the canonical vocabulary.
See [docs/VOCABULARY.md](../../docs/VOCABULARY.md) for the full list with semantics.

Before using a new vocabulary value in an agent YAML:
1. Check it is listed in `docs/VOCABULARY.md`.
2. If missing, add it following the instructions at the top of that document.

## Adding an entry

1. Create `<agent-name>.yaml` in this directory.
2. Set `schema_version: 2`.
3. Fill in `identity`, `capabilities`, and `trust_labels`.
4. Verify every intent, conversation_kind, trust_label, and format value is in `docs/VOCABULARY.md`.
5. Run `go test ./internal/store/...` — the `TestAllFleetEntriesValidateAsV2` test will validate your entry.
