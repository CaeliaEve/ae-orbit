# Durable Production Jobs: Backend Design

Date: 2026-11-11

Status: Approved for implementation by the user's "continue and complete" goal
on 2026-11-11, following the written-specification review request.

## Goal

Accept a complete crafting manifest durably, then admit its entries into the
existing order pipeline with backpressure. After acceptance, closing a browser,
switching devices, or restarting the API must not lose unadmitted entries.

This is durable admission, not recovery of native AE execution handles. It does
not accelerate an individual AE craft or bypass the existing game budgets.

## Existing Evidence

- `web/src/app.mjs`, `submitDraft`: the browser sequentially posts individual
  orders. Closing it stops submission of the remaining local entries.
- `web/src/model.mjs`: drafts are device-local and accept at most 500 entries.
- `internal/store/migrations/012_order_batches.sql`: `batch_id` is only a label.
- `internal/store/orders.go`, `CreateOrder`: a network lock protects canonical
  identity, idempotency, queue capacity, audit and order creation.
- `internal/store/store.go`, `DefaultOptions`: the default queue limit is 256,
  active window is four, and shared planning window is one.
- `internal/store/bridge.go`: assignment, native reports and uncertain-attempt
  handling already have a safety contract that jobs must preserve.
- `internal/store/orders.go`, `Maintain`: terminal order history expires after
  30 days. Job deduplication cannot depend on these orders surviving forever.
- `internal/store/events.go` and `internal/api/events.go`: SSE already has
  persistent replay, reset, coalesced wakeups and permission rechecks. It is not
  an atomic job snapshot or a job-specific delta feed.

## Choice And Scope

Chosen: immutable, server-accepted manifests with bounded admission into the
existing orders. Add authoritative job summaries and a small job-only change
feed. Keep one deployed backend instance and the existing OC protocol.

Rejected: increasing queue limits and retaining browser-driven submission.
Neither makes unfinished intent durable or shared between devices.

Deferred: a general workflow engine, mutable cloud drafts, recipe dependencies,
automatic replenishment, execution failover and a native AE capability bridge.
These are independent projects, not prerequisites for durable job admission.

The first subproject is the backend engine and API. The existing frontend and
its silver-white appearance remain unchanged. A subsequent focused design will
replace browser fan-out with job submission and consume the new sync contract.
Until then, existing browser submissions do not gain these job guarantees.

## Non-Negotiable Safety Boundaries

- Quantity remains an additional crafting amount, never an inventory target.
- No named CPU selection or waiting. The new input has no CPU-policy fields;
  the existing service and Lua capability gates stay disabled.
- No automatic retry of any admitted entry, regardless of its eventual state.
  In particular, `needs_review` is not pending work and must not be resubmitted.
- No atomic all-or-nothing execution promise. Acceptance of the manifest is
  atomic; crafting its entries is incremental and may have mixed outcomes.
- No fabricated material tree, AE percentage, ETA, output attribution, running
  cancellation, or native-handle recovery.
- No production database migration, deployment, crafting, credential changes,
  OC installation, game edits, commits or pushes as part of design validation.

## Accepted Intent And Identity

A creation request contains `request_key`, optional `name`, `ttl_seconds`, and
an ordered array of entries. An entry contains `entry_key`, `item_id` and
`quantity`. IDs, quantities, versions and cursors use exact decimal strings
where the existing protocol uses `Quantity`.

The first version accepts 1-500 entries and at most 256 KiB of JSON. Keys are
8-128 ASCII characters from letters, digits, underscore, hyphen, period and
colon. Names are at most 128 UTF-8 bytes, without control characters. TTL zero
normalizes to 3600 seconds; explicit values are 1-86400 seconds.

Each entry has a stable client source key, a server-generated entry ID, and an
immutable ordinal. Equal items do not imply equal intent: different entry keys
remain separate lines. The service does not merge or silently drop lines.

The normalized input is frozen before admission. Its versioned digest includes
the requested item IDs, normalized quantities and TTL, entry keys, ordinal and
normalized name. Preserve requested IDs separately from resolved canonical IDs
so a later alias change cannot silently change the replay comparison.

Creation is one transaction: verify current owner permission, reconcile the
request receipt, then validate all new identities and cached crafting
eligibility, reserve source-entry keys, and persist the job, entries and
acceptance receipt. For a syntactically valid exact replay, receipt lookup
precedes current eligibility and capacity checks, so later recipe or queue
changes cannot invalidate an earlier acceptance. Any invalid new line or
overlapping source key rejects the entire new manifest without admitting its
other lines.

Scope the request key and source-entry keys to `(member_id, network_id)`.
Replaying the same request key and normalized payload returns the original
acceptance without creating orders. Changed payload returns 409. A different
request key containing an already accepted source key returns
`entry_already_accepted`, with original job IDs visible only to an authorized
owner or administrator.

A lost creation response remains an uncertain acceptance until reconciled by
the original request key. A 404 is not proof that another in-flight creation
cannot commit. Clients must not generate replacement entry keys automatically.

OC availability is not necessary to save intent if the service already has a
known compatible crafting capability and exact, cached craftable identities.
An unknown capability or invalid item rejects creation; an offline compatible
primary leaves accepted entries pending. Cached eligibility is not a promise
that a recipe will still be available when admission or execution occurs.

## Durable Data

Add a new migration after 012. Do not rewrite existing inventory or reinterpret
legacy batch labels as jobs.

| Entity | Responsibility |
| --- | --- |
| `production_jobs` | Owner, network, frozen manifest digest, name, control, versions, counters and settlement timestamps |
| `production_job_entries` | Stable identities, ordinal, requested/canonical item, quantity, admission state and immutable order receipt |
| `production_job_receipts` | Compact durable creation-key/digest/job-ID tombstones independent of detail retention |
| `production_job_entry_receipts` | Unique owner/network source-entry-key ownership, retained after detail cleanup |
| `production_job_changes` | Bounded historical summary snapshots and removal records keyed by network job revision |
| `production_job_actions` | Bounded control-operation receipts and control audit, independent of order progress |
| `production_job_authority_epochs` | Persistent owner/network permission-loss generations, surviving grant deletion and restoration |

Add a nullable unique job-entry association on orders. The association, not
`batch_id`, determines membership and aggregation. Existing single orders and
legacy batches stay valid. Service-generated linked orders may use the job ID
as their display batch label without granting membership to unrelated orders.

An entry stores its admitted order ID and terminal receipt. Losing or cleaning
up a linked order must never make that entry eligible for admission again.
Reuse an extracted transaction-local order-creation primitive; do not call the
public `CreateOrder` inside another transaction.

## Bounded Admission

Only a valid primary poll admits work, in the same transaction before existing
command assignment. Both v1 single mode and v2 primary mode use this path;
quantity workers never admit or execute crafting.
Cached sequence replays and invalid polls cannot admit new entries.

There is no new background craft executor. API restarts preserve pending rows;
the next valid primary poll resumes admission from durable state. Offline
maintenance may pause unauthorized jobs and update summaries, but cannot create
execution attempts.

Initial advertised limits:

| Budget | Limit |
| --- | --- |
| Entries per job | 500 |
| Unsettled jobs per network / per owner in that network | 64 / 16 |
| Detailed retained jobs per network, including unsettled jobs | 512 |
| Accepted creation receipts per network | 16384 |
| Accepted source-entry receipts per network | 250000 |
| Admitted but unsettled entries per job | 8 |
| Queued job-linked orders per network | `min(32, MaxQueuedOrders)` |
| Entry selections per primary poll, including rejected or blocked selections | 16 |
| Job candidates examined per primary poll | 64 |
| Retained summary changes per network | 2048 |
| Changes per response | 100 |
| Retained control operations per detailed job | 1024 |

Creation atomically enforces both receipt ceilings. The lower applicable
remaining capacity wins; the creation-receipt limit alone would otherwise
permit 8,192,000 source-entry tombstones. At either ceiling, reject new manifests
with 429 rather than erase accepted keys. An operator storage expansion or a
separately designed archive workflow is needed to raise these budgets; automatic
receipt deletion is not part of this version. Counts bound rows, not physical
PostgreSQL bytes; measure storage/index costs before increasing these limits.

Keep the existing total queued-order, execution-window, response-byte and
server-group planning budgets. Job-linked queued orders also count toward the
total queue cap. These limits do not reserve an execution slot or promise
immediate precedence over existing direct orders.

Use persisted cursor-based round-robin admission with quantum one. Traverse
active unsettled jobs in stable `(created_at,id)` order, wrap once as necessary,
and advance the cursor after every selected job, including blocked or rejected
selections. New or newly eligible jobs participate on the next traversal of
their position; there is no frozen round-membership promise. Respect both the
entry-selection and examined-job budgets even when nothing can be admitted.
Existing command assignment remains FIFO. This is fairness between job
admissions in one network, not fairness between members or AE networks.

For each selected pending entry, transactionally recheck owner authority,
current capabilities, canonical identity, exactness and cached craftability.
If valid and capacity is available, create one linked order and receipt
atomically. Its TTL starts at admission, not manifest creation.

Capacity, offline state and execution-window pressure leave entries pending.
A permanently invalid identity or incompatible per-item quantity/craftability
marks that never-admitted entry `rejected` with a concrete reason; process other
entries without head-of-line blocking. A missing network-wide crafting service
blocks admission without rejecting every entry. No failed/rejected entry is
automatically retried after capabilities change.

## Lifecycle And Controls

Keep admission control separate from execution state:

- `active`: eligible pending entries may be admitted.
- `paused`: no further admissions; existing queued and executing orders continue.
- `stopped`: cancel unadmitted entries and atomically cancel linked orders that
  are still queued. Preserve assigned, computing, running and uncertain orders.
  Stopping is irreversible; it does not promise that no AE work remains.

Only the owner with current crafting permission, or an authorized
administrator, can pause or stop a job. Resume additionally requires the
original owner to have current crafting authority; it never transfers
ownership or resumes execution under the administrator's identity.

Only active-to-paused, paused-to-active and active/paused-to-stopped transitions
are valid. A settled job cannot change control. Invalid transitions return 409
without recording a new successful operation.

Control requests carry `operation_key` and `expected_control_version`.
Check a stored operation receipt before checking the current precondition.
Exact replays return the original result; changed parameters under the same
key conflict. Admission-control changes, including a durable permission-loss
pause, increment `control_version`. All visible job changes increment the
separate `version`; native progress does not cause otherwise valid control
requests to fail their CAS check.
At the control-operation retention ceiling, reject new operation keys rather
than forget receipts. Reserve one of the 1024 receipt slots for the single
irreversible stop operation: exhausting pause/resume receipts must not prevent
stopping the job. Exact replays remain available while the detailed job is
retained. An archived job cannot execute a control operation, including after
its action receipts are removed.

Lost owner authority prevents new admissions and durably pauses the job with
`owner_permission_lost`. Permission restoration does not resume it silently.
Already admitted orders retain the existing service semantics; revocation
does not claim to undo or cancel game-side work.

Each accepted/resumed job records its owner/network authority epoch. Permission
loss increments that pair's persistent epoch while the permission mutation holds
the member lock; deleting a grant does not delete this epoch. Member revocation
increments every existing epoch for that member. Restoring a grant never clears
the increment. Even revoke-and-restore entirely between polls leaves an epoch
mismatch that forbids admission. Poll/maintenance materializes the paused job
and reason; only explicit authorized resume adopts the current epoch. Authority
changes for another network do not invalidate this network's jobs.

Do not provide retry, force-finish, running-cancel or ownership-transfer job
actions. Existing administrator order review records explicit outcomes using
the existing endpoint. Same-attempt, same-session terminal bridge evidence may
also resolve review exactly as it does today.

## Aggregation And Transaction Boundaries

Entry states partition the manifest exactly:
`pending`, `rejected`, `queued`, `assigned`, `computing`, `running`, `needs_review`,
`done`, `failed`, `canceled`, `expired`.

Counters come from authoritative entry/order state, never from the displayed
history page. Their sum always equals the accepted entry count.
`needs_review` is unsettled and attention-requiring, not successful completion.
Do not sum unlike item quantities into a fake production-progress percentage.

The job is settled only when no entry is pending, queued, assigned, computing,
running or needs_review. Its result is `completed` if all entries are done,
`canceled` if stopping left canceled entries but no failures/rejections/expiry,
and `finished_with_issues` for other settled mixtures. Before settlement the
result is absent. Return exact state counts so the UI need not guess.

In the same transaction as each linked-order mutation, update affected entry
receipts, job counters, version, settlement and the change feed. Coalesce changes
to one summary per affected job per transaction. Cover order creation,
assignment/failure, bridge reports, queued cancellation, administrator review,
bulk uncertainty, bulk expiry and relevant cleanup. Instrumenting only
`auditOrder` would miss the current bulk SQL paths.

Follow existing network serialization before allocating job revisions. Preserve
the bridge lock order: network, device, server group; never lock a group and
then acquire its network. Job/control rows are acquired after the network lock.

Admission and control authorization must be transaction-local. Take shared
member locks in stable ID order before checking grants. SetGrant, DeleteGrant
and RevokeMember must take the corresponding member's exclusive row lock, then
change authorization without subsequently acquiring network locks. This also
serializes absent-grant deletion/insertion, rather than relying on locking a
grant row that does not exist. Do not reuse an earlier HTTP authorization or a
creator token as proof of autonomous admission authority.
While holding that member lock, permission-loss transactions update authority
epochs but do not directly mutate job summaries or their feed. Admission checks
the saved epoch as well as current permission. Any transaction that locks job
rows and member rows must acquire the member locks before the job rows.

## API And Synchronization

Routes below are under `/api/v1/networks/{network}`:

| Method and path | Contract |
| --- | --- |
| `POST /jobs` | Atomic intent acceptance; 201 first acceptance, 200 exact replay |
| `GET /jobs/by-key/{key}?owner_id=...` | Caller-scoped reconciliation; optional owner selector is administrator-only |
| `GET /jobs` | Consistent current unsettled-job summaries, counts, limits and job watermark |
| `GET /jobs/history?cursor=...&limit=...` | Keyset-paginated settled summaries; maximum 100 |
| `GET /jobs/changes?after=...` | Bounded historical summary deltas or explicit reset |
| `GET /jobs/{job}` | Consistent summary and at most 500 immutable entries with authoritative states |
| `POST /jobs/{job}/pause` | Admission-only pause with operation receipt and CAS |
| `POST /jobs/{job}/resume` | Resume pending admission with current owner authorization |
| `POST /jobs/{job}/stop` | Stop future work and withdraw only queued orders |

Network viewers can read jobs as they can read network orders today. Only the
owner/admin can see creation-key reconciliation and control receipts. Use the
existing session/Bearer authentication, Origin/CSRF enforcement, strict JSON
decoding, timeouts, concurrency budgets and structured error conventions.
Creation/control capacity errors include `Retry-After: 1`; no hidden unbounded
work queue or per-member request-rate guarantee is introduced.

Add `networks.jobs_revision`, independent of inventory revision, job `version`,
`control_version`, and SSE event IDs. Increment it transactionally under the
network lock. Store the resulting small historical summary and entity version
in each change row, not a reference joined to a later mutable summary.
Every change row gets its own strictly increasing network revision, even when
one transaction changes multiple jobs. No page can split multiple rows sharing
one scalar cursor value.

The unsettled snapshot uses one repeatable-read transaction for summaries,
counts and its watermark. History pages are individually consistent, not an
unlimited frozen cross-page snapshot. Open-job detail also uses one consistent
read and omits raw NBT. Full entry-level deltas are out of scope.

Changes return ascending revisions, `next_after`, `has_more`, `watermark` and
`reset`. A nonempty page advances only to its last returned revision; an empty
page retains the supplied cursor. Cursors below the retention floor or ahead
of the watermark require a reset. Removal of detailed history emits a tombstone
before deletion; changes themselves retain at most 2048 rows per network.

The existing network SSE sends a new `jobs` wake hint after commit. Clients
pull the dedicated job feed from their last applied jobs cursor. On open,
reconnect, reset, visibility return and periodic fallback, drain that feed;
apply summaries and cursor together. A reset replaces the unsettled snapshot
and watermark, invalidates cached history pages, and then catches up again.
Never advance the jobs cursor just because an SSE event arrived.

## Retention And Compatibility

Keep unsettled jobs, uncertain entries and their receipts. Retain settled
detail for 30 days after settlement, then clean it in bounded maintenance
chunks. Do not evict younger detail merely to admit new jobs at the 512 limit.

Retain referenced terminal orders until their job's detail-retention deadline,
even if an individual order finished earlier. Preserve the existing order
cleanup behavior for unrelated legacy orders. After job-detail cleanup,
creation-key and source-entry-key tombstones still prevent duplicate admission.
Reconciliation returns an explicit archived receipt (job ID and digest, no
editable manifest); it never turns an archived acceptance into a new job.

No automatic conversion of existing browser drafts, uncertain submissions,
orders or display batches. No old endpoint changes its meaning. The new backend
needs no OC update; a future frontend must detect API support rather than
silently fall back to per-entry submission after an uncertain job acceptance.

Deploy only after an explicit approval, a database backup and stopping the old
application writer. Do not run old/new backend writers concurrently. Reverting
an installation containing accepted jobs requires a compatible backend or a
deliberate paired restore, never silently orphaning durable intent.

## Focused Acceptance Evidence

Use the existing isolated real-PostgreSQL schema fixtures, HTTP test harness and
simulator. Do not require a running game for backend acceptance. Follow TDD:
observe the relevant test fail before implementing each behavior.

1. Accept 500 entries with a deliberately small queue budget. Assert all intent
   is durable while queue/outstanding limits remain intact.
2. Close/reopen Store and HTTP instances using the same isolated schema.
   Resume only unadmitted entries; admitted IDs and uncertain attempts do not
   change and no duplicate order appears.
3. Race exact creation replay and overlapping source-entry submissions. Assert
   one acceptance or an atomic whole-manifest conflict, never partial fan-out.
4. Exercise round-robin admission with competing jobs and direct orders;
   preserve total queue, active and server-group planning limits.
5. Pause and stop concurrent with primary dispatch. Assert precise queued versus
   assigned boundaries, control CAS and response-loss receipt replay.
6. Revoke/delete/downgrade authority while admission runs. Assert no admission
   after the permission transaction wins, explicit resume after restoration,
   and no bypass when revoke-and-restore both happen between primary polls.
7. Exercise every linked-order transition, including bulk expiry/uncertainty
   and same-session terminal review resolution. Counts partition the manifest
   and versions/changes commit atomically.
8. Test snapshot-to-feed races, more than 100 changes, reset floors, invalid or
   ahead cursors, coalesced SSE wakeups and history-removal tombstones.
9. Run retention against an old completed entry in a still-unsettled job.
   Preserve its receipt and deduplication before and after detail archival.
10. Verify strict request limits, CSRF/read-only access, foreign-network items,
    exact integers, no CPU-policy bypass and unchanged legacy single orders.

Keep tests at the storage/API boundaries; do not add a generic load-test or
regression framework. Report database-test skips explicitly. Offline tests do
not prove real crafting throughput, game TPS, CPU targeting, or handle recovery.

## Review Outcome And Next Gate

Read-only store and API/frontend audits agree on the architecture and highlighted
the retention, bulk-transition, authorization and cursor issues addressed above.
No test, deployment or crafting result is claimed by this document.

Execute the implementation plan for this backend subproject with focused
red/green tasks and independent review. Frontend adoption will have its own
small design and verification cycle.
