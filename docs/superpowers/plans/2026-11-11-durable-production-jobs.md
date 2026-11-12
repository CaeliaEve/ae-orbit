# Durable Production Jobs Implementation Plan

> For agent workers: use subagent-driven-development, one implementation task at
> a time, followed by independent specification and quality review. Never use
> gpt-5.6-sol. Preserve the existing dirty, detached managed worktree.

**Goal:** Finish the approved backend production-job engine and HTTP contract,
including durable acceptance, bounded primary-only admission, controls, complete
aggregation, authorization fencing, retained deduplication and summary deltas.

**Architecture:** Keep immutable manifests separate from existing orders.
Network-serialized transactions link admitted entries to exactly one order.
Coalesce dirty-job summaries before transaction commit and publish durable
summary changes with an independent network jobs revision.

**Stack:** Existing Go, pgx, PostgreSQL, net/http, simulator and isolated real
database tests. No OC or frontend dependency changes.

## Authority And Global Constraints

Specification: `docs/superpowers/specs/2026-11-11-durable-production-jobs-design.md`.
The user approved implementation with the active goal "continue and complete".
Do not stop between tasks to request another execution-method selection.

- No live deployment, live migration, real crafting, credential changes, OC/game
  changes, commits, pushes, unrelated reverts, or database resets.
- Use `apply_patch` for manual edits. All test database writes belong to random
  schemas in a verified dedicated test database.
- No named CPU targeting, target-stock semantics, native handle recovery, or
  automatic retry of any admitted entry.
- Preserve existing order attempt/session gates and legacy endpoint behavior.
- Every explicit requirement in the specification needs implementation and
  fresh evidence before marking the backend goal achieved.
- Keep task progress and review evidence in this plan's private SDD workspace.
- Do not remove the managed worktree or temporary evidence on completion.

## Files And Interfaces

Add:

- `internal/protocol/production_jobs.go`: input/view types, normalization,
  validation and advertised initial budgets.
- `internal/protocol/production_jobs_test.go`: precise input behavior.
- `internal/store/migrations/013_production_jobs.sql`: new entities, job-entry
  order association, jobs revision and persistent admission cursor.
- `internal/store/production_jobs.go`: acceptance, identity and detail reads.
- `internal/store/production_job_state.go`: dirty tracking, entry projection,
  authoritative summary aggregation and durable change publication.
- `internal/store/production_job_admission.go`: bounded primary-poll admission.
- `internal/store/production_job_controls.go`: control CAS/receipts and authority.
- `internal/store/production_job_sync.go`: snapshots, history, changes and cleanup.
- Focused storage tests grouped with those responsibilities.
- `internal/api/production_jobs.go` and its focused HTTP tests.
- `contracts/production-jobs.md`: implemented wire behavior and operational limits.

Modify only needed integration points:

- `internal/store/store.go`: new errors and precommit dirty-job flush.
- `internal/store/notifications.go`: transaction dirty-job metadata.
- `internal/store/orders.go`: shared enqueue helper, transition marking and
  linked-order retention.
- `internal/store/bridge.go`, `cooperative_reads.go`: primary admission and
  complete linked-order transition marking.
- `internal/store/auth.go`, `sessions.go`: member-row serialization and durable
  owner/network permission-loss epochs.
- `internal/api/routes.go`, `server.go`, `metrics.go`: routes, conflict codes,
  Retry-After and fixed route metrics.
- `contracts/openapi.yaml`, `README.md`: new API and actual implemented scope.

Public storage interfaces:

```go
CreateProductionJob(ctx context.Context, userID, networkID string,
    in protocol.ProductionJobInput) (protocol.ProductionJobAcceptance, error)
ProductionJobByKey(ctx context.Context, userID, networkID, ownerID, key string) (
    protocol.ProductionJobAcceptance, error)
ProductionJob(ctx context.Context, userID, networkID, jobID string) (
    protocol.ProductionJobDetail, error)
ControlProductionJob(ctx context.Context, userID, networkID, jobID, action string,
    in protocol.ProductionJobControlInput) (protocol.ProductionJobSummary, error)
ProductionJobs(ctx context.Context, userID, networkID string) (
    protocol.ProductionJobSnapshot, error)
ProductionJobHistory(ctx context.Context, userID, networkID, cursor string,
    limit int) (protocol.ProductionJobHistoryPage, error)
ProductionJobChanges(ctx context.Context, userID, networkID string,
    after protocol.Quantity) (protocol.ProductionJobChangePage, error)
```

Acceptance contains `receipt` (job ID, manifest digest, archived flag),
optional consistent `job`, and an internal replay flag used for HTTP 200/201.
Summary contains ID/network/owner/name, control, version/control_version,
total, exact per-state counts, result/settled_at, reason and timestamps.
Detail embeds a summary and its bounded entries. Input entry keys are stable
owner/network identities; order associations use server entry IDs, not labels.
A job starts with version 1 and control_version 1.

New transaction helpers are private and named consistently across workers:

```go
markProductionJobChanged(tx pgx.Tx, networkID, jobID string)
markProductionOrderChanged(ctx context.Context, tx pgx.Tx, orderID string) error
flushProductionJobs(ctx context.Context, tx *notifyingTx) error
admitProductionJobs(ctx context.Context, tx pgx.Tx, networkID string,
    caps protocol.Capabilities) error
```

The precommit flush is a no-op for unrelated transactions and migrations.
It projects linked order outcomes, aggregates at most 500 entries per dirty
job, increments one job version, and allocates one unique jobs revision per
summary. Persist historical summary JSON in the feed before commit.

## Task 1: Atomic Acceptance And Durable Identity

Files: protocol types/tests, migration 013, production_jobs.go,
production_job_state.go, store.go, notifications.go and acceptance tests.

- [x] Write table tests rejecting empty/>500 entries, duplicate source keys,
  invalid ASCII keys, invalid UTF-8/control names, TTL and exact integer errors.
  Assert TTL 0/3600 normalize identically and meaningful input order is retained.
- [x] Run the protocol tests and record the expected missing-behavior RED.
- [x] Write a storage test using the existing `orderFixture`:

```go
input := protocol.ProductionJobInput{
    RequestKey: "job-create-001",
    Entries: []protocol.ProductionJobEntryInput{
        {EntryKey: "source-entry-001", ItemID: itemID, Quantity: 64},
    },
}
accepted, err := db.CreateProductionJob(ctx, admin.ID, network.ID, input)
// Assert total=1, pending=1, no orders exist, exact replay keeps the same job.
// Changed quantity under the key conflicts; a second key reusing the source
// entry fails atomically and does not create a partial job.
```

- [x] Observe RED before production changes, then implement validated normalized
  inputs, atomic acceptance, scoped receipts and consistent bounded detail.
- [x] Enforce receipt/live/detail ceilings before new creation, but after
  syntactically valid exact-replay reconciliation. Preserve input identity
  independently of alias resolution; reject unsupported/non-exact new items.
- [x] Add the schema and precommit aggregation/change foundation. Protect
  network serialization and use member SHARE locks before job rows.
- [x] Verify exact replay after capability changes, offline intent acceptance,
  viewer/cross-network rejection, concurrent overlap, 500-entry persistence,
  and replay/entry deduplication independent of order history.
- [x] Run focused protocol/storage tests, format and independently review the
  task diff with its RED/GREEN report. Do not commit.

## Task 2: Admission, Controls And Complete Lifecycle

Files: admission/controls modules and tests, order enqueue helper,
bridge.go/cooperative_reads.go, auth.go/sessions.go, bulk order transitions.

- [x] Add RED tests using a small configured queue cap. Drive valid primary
  polls and assert one order per admitted source entry and pending leftovers:

```go
ack, err := db.Poll(ctx, device, protocol.Poll{
    SessionID: welcome.SessionID, Sequence: 1,
})
// Total queued <= MaxQueuedOrders, outstanding/job <=8, job queue <=32.
// Repeating exactly this poll creates no new orders or attempts.
// A quantity-worker poll cannot admit any work.
```

- [x] Extract the existing transaction-local enqueue logic while preserving
  CreateOrder idempotency/capability/batch checks and its public behavior.
- [x] Implement persistent cursor-based quantum-one job admission before
  assignment in new valid v1/v2 primary polls. Bound entry selections (16,
  including rejection/blocking) and examined jobs (64); preserve FIFO order
  dispatch and all shared planning/response budgets.
- [x] Add RED control tests for pause-only-future-admission, stop canceling only
  pending/queued, invalid/settled transitions, stale control_version and exact
  operation replay before CAS. Implement action receipts with reserved stop slot.
- [x] Add RED revoke/restore-between-polls tests. Implement permission epochs
  under exclusive member locks in grant deletion/downgrade/revocation. Job
  creation/resume records the epoch; admission fences mismatches, then pauses.
- [x] Mark every single and bulk linked-order transition dirty. Flush correct
  entry states, counts, summary versions and settlement atomically. Bulk SQL
  must return affected linked job IDs instead of relying only on auditOrder.
- [x] Verify expired/failed/canceled/review entries are never re-admitted,
  primary restart preserves uncertainty, same-session terminal evidence resolves
  review, and API Store restart resumes only never-admitted entries.
- [x] Run focused lifecycle/concurrency and existing order/cooperation tests;
  independently review the task diff and fix confirmed findings. Do not commit.

## Task 3: Consistent Sync, Retention And HTTP Delivery

Files: sync module/tests, API handlers/tests, routes/errors/metrics,
retention integration, OpenAPI and concise user-facing contract/docs.

- [x] Write RED tests for snapshot watermark consistency, historical summaries,
  >100 delta rows, unique per-change revisions, retention-floor/ahead resets,
  empty-cursor behavior, history pagination and removal tombstones.
- [x] Implement repeatable-read snapshot/detail/history/change reads. Expose all
  initial budgets, ascending changes and last-returned next_after. Do not confuse
  inventory revision, entity version, control version or SSE IDs.
- [x] Write RED retention tests for old terminal orders in unsettled jobs and
  archived accepted manifests. Implement bounded 30-day settled-detail cleanup,
  order retention, authority-pausing maintenance and independent tombstones.
- [x] Add strict HTTP route tests:

```go
// POST /jobs first returns201, exact replay200.
// GET by-key uses caller owner by default, admin-only owner_id override.
// GET snapshot/detail/history/changes works for network viewers.
// POST pause/resume/stop rejects missing CSRF, nonowner, stale CAS and CPU fields.
// Feed response contains decimal-string versions/cursors and archived receipts
// cannot become new jobs. New job-capacity failures include Retry-After.
```

- [x] Register handlers without ServeMux by-key/action ambiguity. Reuse user
  authentication, limits, strict JSON, existing errors and fixed route metrics.
  Emit coalesced `jobs` SSE hints after durable changes commit.
- [x] Document exact wire shapes, paused/stopped semantics, limits, archive
  behavior, offline validation scope and unchanged frontend/OC behavior.
- [x] Run real PostgreSQL Go tests without skips, targeted race tests if a
  compatible compiler is available, `go vet ./...`, contract verification,
  existing frontend tests/build and `git diff --check`.
- [x] Perform isolated HTTP/simulator process restart verification against the
  same test schema, without using the user's live network or credentials.
- [x] Obtain final independent scope/quality review; fix findings and rerun
  relevant tests. Audit the specification requirement by requirement.
- [x] Record actual evidence and remaining deployment/frontend boundaries.
  Only mark the backend goal complete after all backend requirements are proven.

## Execution Evidence

Backend implementation and offline acceptance completed on 2026-11-11.

- Full serial real-PostgreSQL Go suite: 557 passing tests/subtests, zero failed
  or skipped individual tests. Four packages contain no test files.
- Final-state production-job protocol/store/API suite: 93 passing tests/subtests,
  zero failures/skips, including both final corrections.
- Seven selected concurrency groups passed Go race detection (13.747s).
- Full go vet, contract verification (51 operations/40 examples), frontend
  tests (37), frontend build, embedded web tests and diff checks passed.
- Fresh isolated backend/simulator processes verified pending acceptance,
  original-key reconciliation and watermark survival across restart; admitted
  order/attempt identities remain stable and uncertain work is not reissued.
  A separate synthetic manifest completed all entries exactly once. All owned
  processes stopped and the isolated schema was removed.
- Independent task and final broad reviews passed. Cap-one fairness, deletion
  entity version and a notification-test false-positive risk were corrected;
  no open findings remain.
- Grouped missing-API/runtime RED evidence and subsequent GREEN are recorded.
  Supplemental cases added after implementation are not claimed as independent
  per-case runtime TDD.

Detailed evidence remains in this plan's private SDD workspace. The original
dirty managed worktree is preserved without commits or pushes. No deployment,
live migration, OC/game changes or real crafting occurred. Existing frontend
submission still uses legacy orders until separately integrated with the job
API; this work does not establish game throughput, CPU targeting or native
execution-handle recovery.
