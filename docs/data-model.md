# Data model

PostgreSQL 16+ is the implemented operational and governance store. Migrations
and `docs/database.md` are authoritative for exact physical names.

## Architecture

This is a normalized relational operational model with dimensional read views.
It is not a star schema: execution, evidence, governance, and retention records
retain transactional integrity in third normal form, while views provide stable
analytics-facing projections. PostgreSQL remains the sole active store. Parquet
and Delta Lake are intentionally excluded from local transactional persistence;
they may be considered later only as optional export formats.

The physical baseline contains 32 tables. Polymorphic `execution_events` and
`observations` consolidate structurally similar facts without storing arbitrary
domain payloads. Typed kinds, checks, foreign keys, native units, provenance,
knowledge state, and evidence links preserve statistical meaning.

## Entity relationship diagram

GitHub renders this Mermaid diagram directly. Run
`go run ./scripts/generate-erd.go` after changing any migration. The generator
discovers every ordered migration up-section, including future tables and added
columns, but rewrites artifacts only when the physical ERD changes. The
pre-commit gate updates the same DBML, SVG, and generated Markdown block; CI
rejects drift and compares the result with the live PostgreSQL catalog.

<!-- BEGIN GENERATED ERD -->

_Generated from all ordered migration up-sections; schema fingerprint `4d77d11861a5`._

[![Complete physical ERD with columns, types, keys, and relationships](data-model-erd.svg)](data-model-erd.svg)

Open the SVG for a zoomable complete physical model, or edit/import [`data-model.dbml`](data-model.dbml) in a DBML-compatible editor. Both files are updated in place. The bounded diagrams below remain readable in GitHub.

### Identity and collection

```mermaid
erDiagram
    projects {
        uuid project_id PK
        timestamptz created_at
    }
    project_versions {
        uuid project_version_id PK
        uuid project_id FK
        bigint version_number
        text classification
        text lifecycle_state
        bytea version_hash
        timestamptz valid_from
        timestamptz valid_to
    }
    sources {
        uuid source_id PK
        text source_kind
        timestamptz created_at
    }
    source_versions {
        uuid source_version_id PK
        uuid source_id FK
        bigint version_number
        text adapter_version
        text product_surface
        text coverage_state
        boolean enabled
        bytea version_hash
        timestamptz valid_from
        timestamptz valid_to
    }
    key_versions {
        uuid key_version_id PK
        text key_reference
        text algorithm
        text state
        timestamptz created_at
        timestamptz activated_at
        timestamptz retired_at
        timestamptz deleted_at
    }
    project_aliases {
        uuid project_alias_id PK
        uuid project_id FK
        uuid source_id FK
        uuid key_version_id FK
        text alias_kind
        bytea alias_digest
        timestamptz created_at
        timestamptz deleted_at
    }
    collection_cursors {
        uuid collection_cursor_id PK
        uuid source_id FK
        text cursor_kind
        text cursor_value
        text cursor_state
        timestamptz observed_at
        timestamptz lease_expires_at
        timestamptz updated_at
    }
    sources ||--o{ collection_cursors : "source_id to source_id"
    key_versions ||--o{ project_aliases : "key_version_id to key_version_id"
    projects ||--o{ project_aliases : "project_id to project_id"
    sources ||--o{ project_aliases : "source_id to source_id"
    projects ||--o{ project_versions : "project_id to project_id"
    sources ||--o{ source_versions : "source_id to source_id"
```

### Execution

```mermaid
erDiagram
    sessions {
        uuid session_id PK
        uuid project_id FK
        uuid source_id FK
        timestamptz started_at
        timestamptz ended_at
        text coverage_state
        text knowledge_state
    }
    tasks {
        uuid task_id PK
        uuid project_id FK
        uuid source_id FK
        uuid external_alias_id
        text task_kind
        text attribution_state
        double_precision confidence
        text algorithm_version
        timestamptz observed_at
        text knowledge_state
    }
    trajectories {
        uuid trajectory_id PK
        uuid session_id FK
        uuid source_id FK
        uuid external_alias_id
        uuid parent_trajectory_id FK
        timestamptz started_at
        timestamptz ended_at
        text knowledge_state
    }
    turns {
        uuid turn_id PK
        uuid trajectory_id FK
        uuid task_id FK
        uuid source_id FK
        uuid external_alias_id
        bigint ordinal
        timestamptz observed_at
        text knowledge_state
    }
    responses {
        uuid response_id PK
        uuid turn_id FK
        uuid source_id FK
        uuid external_alias_id
        uuid parent_response_id FK
        text model_name
        text model_variant
        text reasoning_effort
        text reasoning_mode
        text reasoning_context
        text verbosity
        text service_mode
        text cache_mode
        bigint cache_ttl_seconds
        text safeguard_outcome
        timestamptz started_at
        timestamptz completed_at
        text knowledge_state
    }
    items {
        uuid item_id PK
        uuid response_id FK
        uuid source_id FK
        uuid external_alias_id
        text item_kind
        text assistant_phase
        bigint ordinal
        text image_detail
        bytea content_hash
        bigint content_length
        timestamptz observed_at
        text knowledge_state
    }
    phases {
        uuid phase_id PK
        uuid trajectory_id FK
        uuid response_id FK
        text phase_kind
        bigint ordinal
        timestamptz started_at
        timestamptz ended_at
        text knowledge_state
    }
    state_epochs {
        uuid state_epoch_id PK
        uuid trajectory_id FK
        uuid source_id FK
        bytea state_hash
        text mutation_state
        timestamptz started_at
        timestamptz ended_at
        text knowledge_state
    }
    tool_calls {
        uuid tool_call_id PK
        uuid response_id FK
        uuid phase_id FK
        uuid item_id FK
        uuid state_epoch_id FK
        uuid source_id FK
        uuid external_alias_id
        uuid caller_alias_id
        uuid program_output_alias_id
        bytea canonical_call_hash
        text call_path
        text tool_kind
        text result_state
        text output_modality
        bigint output_size_bytes
        text wait_state
        timestamptz started_at
        timestamptz completed_at
        text outcome
        text knowledge_state
    }
    execution_events {
        uuid execution_event_id PK
        text event_kind
        text event_name
        text schema_version
        uuid project_id FK
        uuid session_id FK
        uuid trajectory_id FK
        uuid parent_trajectory_id FK
        uuid response_id FK
        uuid phase_id FK
        uuid source_id FK
        uuid evidence_artifact_id FK
        uuid external_alias_id
        uuid related_event_id FK
        text outcome
        text state_value
        bigint depth
        numeric numeric_value
        double_precision confidence
        bytea content_hash
        jsonb attributes
        timestamptz observed_at
        text knowledge_state
    }
    evidence_artifacts ||--o{ execution_events : "evidence_artifact_id to evidence_artifact_id"
    trajectories o|--o{ execution_events : "trajectory_id to parent_trajectory_id"
    phases o|--o{ execution_events : "phase_id to phase_id"
    projects o|--o{ execution_events : "project_id to project_id"
    execution_events o|--o{ execution_events : "execution_event_id to related_event_id"
    responses o|--o{ execution_events : "response_id to response_id"
    sessions o|--o{ execution_events : "session_id to session_id"
    sources o|--o{ execution_events : "source_id to source_id"
    trajectories o|--o{ execution_events : "trajectory_id to trajectory_id"
    responses ||--o{ items : "response_id to response_id"
    sources ||--o{ items : "source_id to source_id"
    responses o|--o{ phases : "response_id to response_id"
    trajectories ||--o{ phases : "trajectory_id to trajectory_id"
    responses o|--o{ responses : "response_id to parent_response_id"
    sources ||--o{ responses : "source_id to source_id"
    turns ||--o{ responses : "turn_id to turn_id"
    projects o|--o{ sessions : "project_id to project_id"
    sources ||--o{ sessions : "source_id to source_id"
    sources ||--o{ state_epochs : "source_id to source_id"
    trajectories ||--o{ state_epochs : "trajectory_id to trajectory_id"
    projects o|--o{ tasks : "project_id to project_id"
    sources ||--o{ tasks : "source_id to source_id"
    items o|--o{ tool_calls : "item_id to item_id"
    phases o|--o{ tool_calls : "phase_id to phase_id"
    responses ||--o{ tool_calls : "response_id to response_id"
    sources ||--o{ tool_calls : "source_id to source_id"
    state_epochs o|--o{ tool_calls : "state_epoch_id to state_epoch_id"
    trajectories o|--o{ trajectories : "trajectory_id to parent_trajectory_id"
    sessions ||--o{ trajectories : "session_id to session_id"
    sources ||--o{ trajectories : "source_id to source_id"
    sources ||--o{ turns : "source_id to source_id"
    tasks o|--o{ turns : "task_id to task_id"
    trajectories ||--o{ turns : "trajectory_id to trajectory_id"
```

### Evidence

```mermaid
erDiagram
    evidence_artifacts {
        uuid evidence_artifact_id PK
        uuid source_id FK
        uuid project_id FK
        uuid session_id FK
        uuid external_alias_id
        text schema_version
        bytea content_hash
        bigint content_length
        text classification
        text redaction_state
        text coverage_state
        text provenance
        text product_surface
        timestamptz observed_at
        timestamptz retained_until
        timestamptz deleted_at
    }
    evidence_links {
        uuid evidence_link_id PK
        uuid evidence_artifact_id FK
        text target_kind
        uuid target_id
        text link_kind
        double_precision confidence
        timestamptz created_at
    }
    observations {
        uuid observation_id PK
        text observation_kind
        text schema_version
        uuid project_id FK
        uuid source_id FK
        uuid trajectory_id FK
        uuid response_id FK
        uuid evidence_artifact_id FK
        uuid audit_revision_id FK
        text metric_kind
        text state_value
        numeric value_numeric
        text native_unit
        text product_surface
        text accounting_regime
        text provenance
        text source_adapter
        text source_version
        double_precision confidence
        timestamptz valid_from
        timestamptz valid_to
        jsonb attributes
        timestamptz observed_at
        text knowledge_state
    }
    projects o|--o{ evidence_artifacts : "project_id to project_id"
    sessions o|--o{ evidence_artifacts : "session_id to session_id"
    sources ||--o{ evidence_artifacts : "source_id to source_id"
    evidence_artifacts ||--o{ evidence_links : "evidence_artifact_id to evidence_artifact_id"
    audit_revisions o|--o{ observations : "audit_revision_id to audit_revision_id"
    evidence_artifacts o|--o{ observations : "evidence_artifact_id to evidence_artifact_id"
    projects o|--o{ observations : "project_id to project_id"
    responses o|--o{ observations : "response_id to response_id"
    sources o|--o{ observations : "source_id to source_id"
    trajectories o|--o{ observations : "trajectory_id to trajectory_id"
```

### Governance

```mermaid
erDiagram
    audit_windows {
        uuid audit_window_id PK
        uuid project_id FK
        text window_kind
        timestamptz starts_at
        timestamptz ends_at
        timestamptz as_of
        text timezone_name
        timestamptz immutable_since
        text policy_schema_version
        bytea policy_hash
        boolean raw_retention_enabled
        boolean telemetry_enabled
    }
    audit_revisions {
        uuid audit_revision_id PK
        uuid audit_window_id FK
        bigint revision_number
        uuid prior_revision_id FK
        timestamptz source_watermark_at
        text coverage_state
        bytea revision_hash
        text engine_version
        timestamptz created_at
    }
    evaluation_runs {
        uuid evaluation_run_id PK
        uuid audit_revision_id FK
        text evaluator_name
        text evaluator_version
        bytea fixture_hash
        bytea config_hash
        bytea model_hash
        bytea compiler_hash
        bytea policy_hash
        bytea metric_hash
        bytea run_hash
        text decision
        timestamptz started_at
        timestamptz completed_at
        text outcome
        text knowledge_state
    }
    metric_definitions {
        uuid metric_definition_id PK
        text metric_name
        text metric_version
        text native_unit
        text polarity
        bigint minimum_sample
        double_precision practical_change
        bytea definition_hash
        jsonb definition_json
        timestamptz created_at
        timestamptz retired_at
    }
    metric_results {
        uuid metric_result_id PK
        uuid audit_revision_id FK
        uuid evaluation_run_id FK
        uuid metric_definition_id FK
        text metric_name
        text metric_version
        numeric native_value
        text native_unit
        numeric numerator
        numeric denominator
        bigint sample_count
        text coverage_state
        text provenance
        text status
        text uncertainty
        numeric previous_value
        numeric rolling_median
        numeric rolling_mad
        bigint baseline_sample_count
        numeric workload_adjusted_residual
        text status_reason
        text confidence_label
        jsonb exclusions
        timestamptz computed_at
    }
    findings {
        uuid finding_id PK
        uuid audit_revision_id FK
        uuid evaluation_run_id FK
        text finding_kind
        text detector_name
        text detector_version
        double_precision confidence
        text status
        text cause
        text exception_check
        jsonb counterevidence
        timestamptz created_at
        timestamptz deleted_at
    }
    recommendations {
        uuid recommendation_id PK
        uuid audit_revision_id FK
        uuid finding_id FK
        uuid project_id FK
        text recommendation_kind
        text lifecycle_state
        boolean approval_required
        text verification_kind
        text action_code
        text policy_version
        timestamptz cooldown_until
        text evidence_revision
        text target_surface
        text action_text
        text expected_movement
        jsonb protected_guardrails
        jsonb risks
        timestamptz created_at
        timestamptz updated_at
        timestamptz deleted_at
    }
    recommendation_events {
        uuid recommendation_event_id PK
        uuid recommendation_id FK
        text event_kind
        text prior_state
        text next_state
        timestamptz observed_at
        uuid evidence_artifact_id FK
    }
    audit_windows ||--o{ audit_revisions : "audit_window_id to audit_window_id"
    audit_revisions o|--o{ audit_revisions : "audit_revision_id to prior_revision_id"
    projects o|--o{ audit_windows : "project_id to project_id"
    audit_revisions ||--o{ evaluation_runs : "audit_revision_id to audit_revision_id"
    audit_revisions ||--o{ findings : "audit_revision_id to audit_revision_id"
    evaluation_runs o|--o{ findings : "evaluation_run_id to evaluation_run_id"
    audit_revisions ||--o{ metric_results : "audit_revision_id to audit_revision_id"
    evaluation_runs o|--o{ metric_results : "evaluation_run_id to evaluation_run_id"
    metric_definitions o|--o{ metric_results : "metric_definition_id to metric_definition_id"
    evidence_artifacts o|--o{ recommendation_events : "evidence_artifact_id to evidence_artifact_id"
    recommendations ||--o{ recommendation_events : "recommendation_id to recommendation_id"
    audit_revisions ||--o{ recommendations : "audit_revision_id to audit_revision_id"
    findings o|--o{ recommendations : "finding_id to finding_id"
    projects o|--o{ recommendations : "project_id to project_id"
```

### Retention

```mermaid
erDiagram
    retention_policies {
        uuid retention_policy_id PK
        uuid project_id FK
        text policy_version
        text classification
        bigint retain_for_seconds
        boolean legal_hold
        timestamptz created_at
        timestamptz retired_at
    }
    retention_actions {
        uuid retention_action_id PK
        uuid retention_policy_id FK
        uuid project_id FK
        bytea action_key
        text mode
        timestamptz cutoff_at
        bigint planned_count
        bigint applied_count
        text status
        timestamptz started_at
        timestamptz completed_at
    }
    archive_batches {
        uuid archive_batch_id PK
        uuid project_id FK
        uuid retention_action_id FK
        text archive_reference
        text encryption_key_reference
        text format_version
        timestamptz range_start
        timestamptz range_end
        bigint entity_count
        bytea plaintext_digest
        bytea encrypted_digest
        text status
        timestamptz created_at
        timestamptz verified_at
    }
    retention_action_entities {
        uuid retention_action_entity_id PK
        uuid retention_action_id FK
        uuid archive_batch_id FK
        text entity_kind
        bigint planned_count
        bigint archived_count
        bigint deleted_count
        bytea archive_digest
        bytea deletion_digest
        timestamptz recorded_at
    }
    projects ||--o{ archive_batches : "project_id to project_id"
    retention_actions ||--o{ archive_batches : "retention_action_id to retention_action_id"
    archive_batches o|--o{ retention_action_entities : "archive_batch_id to archive_batch_id"
    retention_actions ||--o{ retention_action_entities : "retention_action_id to retention_action_id"
    projects ||--o{ retention_actions : "project_id to project_id"
    retention_policies ||--o{ retention_actions : "retention_policy_id to retention_policy_id"
    projects o|--o{ retention_policies : "project_id to project_id"
```

<!-- END GENERATED ERD -->

## Migration discipline

All persisted changes use immutable, versioned migrations. For each migration
sequence: create types/extensions when justified, then tables, then indexes,
foreign keys, and views. Never create an index, FK, or view before its referenced
tables. Migrations need forward, rollback/repair, empty-database, upgrade, and
restore validation. Runtime code must not create schema ad hoc.

## Identity and joins

Internal primary keys are UUIDs generated by Prompt Better. Natural identifiers
remain scoped to a source and are unique as `(source_id, external_id)`. All
timestamps are `timestamptz` in UTC. Content-based deduplication uses a versioned
canonicalization algorithm plus SHA-256 hash. Nullable joins must preserve
unmatched evidence rather than invent relationships.

| Entity | Purpose | Principal joins |
|---|---|---|
| `projects` | Configured project boundary. | `project_id` to sessions, policies, reports. |
| `sources` | Evidence adapter/config identity. | `source_id` to cursors and artifacts. |
| `collection_cursors` | Per-source incremental watermark and lease. | unique `source_id + cursor_kind`. |
| `sessions` | Normalized Codex execution session metadata. | `project_id`; external identity through source refs. |
| `tasks` | Opaque normalized task identity and attribution state. | project/source; turns. |
| `state_epochs` | Redacted state-change boundary for repeat classification. | trajectory/source; tool calls. |
| `execution_events` | Typed delegation, compaction, stop, checkpoint, boundary, plan, budget, policy, and capability facts. | evidence/project/session/trajectory/response/phase/source. |
| `tool_calls` | Sanitized tool metadata and state outcome. | response, source, state epoch. |
| `evidence_artifacts` | Normalized envelope and classification. | `source_id`; optional project/session. |
| `evidence_links` | Typed many-to-many provenance links. | artifact to target entity, with confidence. |
| `observations` | Typed usage, cache, confounder, governance, attribution, and source assertions. | trajectory/response/artifact/audit/source. |
| `metric_definitions` / `metric_results` | Versioned formulas and transparent results. | audit revision/evaluation; evidence through typed links. |
| `audit_windows` / `audit_revisions` | Bounded immutable audit window and revision. | project/policy/watermark. |
| `findings` / `recommendations` | Evidence-backed diagnosis and preview-only action lifecycle, retained as one audit-revision unit. | audit revision/evaluation/project. |
| `retention_actions` / `retention_action_entities` | Auditable archive/deletion outcome. | policy, project, archive batch, affected entity class. |

## Sensitive content

Raw prompt bodies and raw source payloads have no default storage column. The
default stores hashes, lengths, classifications, diagnostics, and derived facts.
Any future raw-content table requires a separate migration, explicit opt-in
policy, encryption and deletion design, access audit, security-risk-model
update, and ADR. Secrets must never be stored.

## Views

Views expose project/session coverage, collection freshness, trajectory usage,
tool-loop summaries, token deltas, and governance trends. Views must use normalized tables, preserve
unknown/missing states, document denominator rules, and avoid re-identifying
redacted data. Materialized views, if added, need explicit refresh ownership.

## Retention and recovery

Retention applies by classification, source, project, and age with dry-run and
auditable counts. Deletion follows dependency order and respects legal/user holds.
Backup/restore documentation must cover schema version, encryption, least
privilege, integrity verification, and a tested restore into an isolated database.
