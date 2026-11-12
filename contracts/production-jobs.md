# Durable Production Jobs

The backend accepts immutable manifests and admits them incrementally through
the existing order queue. Quantities are additional craft amounts, not inventory
targets. Acceptance is atomic; execution is not. The frontend submits new
single and batch intents through one immutable job manifest. Legacy orders, display
batch labels, OC protocol, CPU-targeting gates and game behavior are unchanged.

## Requests And Receipts

All routes are under `/api/v1/networks/{network}` and use existing member Bearer
or browser-cookie authentication. Cookie mutations require matching Origin and
X-CSRF-Token. Network viewers can read snapshot, history, detail and changes.

`POST /jobs` accepts at most 256 KiB:

```json
{
  "request_key": "manifest-001",
  "name": "Stone",
  "ttl_seconds": 3600,
  "entries": [{"entry_key": "source-001", "item_id": "1", "quantity": "64"}]
}
```

Keys are 8-128 ASCII letters/digits/underscore/hyphen/period/colon. Entries are
ordered, distinct by source key and never merged by equal item. There must be
1-500 entries. Names default to empty and allow at most 128 valid UTF-8 bytes,
without control characters. TTL omitted or zero normalizes to 3600; explicit
nonzero TTL is 1-86400 seconds and starts on admission. Unknown fields, including
CPU-policy fields, are rejected. Item IDs and quantities are positive exact
decimal strings in the signed 64-bit range.

First acceptance returns 201; exact normalized replay returns 200. Both return
`{"receipt":{"job_id":"...","manifest_digest":"...","archived":false},"job":{...}}`.
There is no JSON `replayed` field. `job` is `{summary,entries}`; entries include
stable `id`, `entry_key`, zero-based integer `ordinal`, `requested_item_id`,
canonical `item_id`, `quantity`, authoritative `state`, and optional `reason`,
`order_id`, `attempt_id`, `terminal_at`. Raw NBT is never returned.

Request/source keys are scoped to owner and network. Changed payload under the
same request key returns 409 `conflict`; another request using an accepted
source key returns 409 `entry_already_accepted` with authorized `error.job_ids`.
Capacity errors return 429 with `Retry-After: 1`. Accepted keys are not erased
to make room.

Browser job source keys are exactly `draft-${row.id}`, matching legacy order
idempotency keys. After exact job replay lookup, new acceptance rejects any
source matching a retained order for that owner/network with HTTP 409 `conflict`,
no job IDs and no receipt. Both writers use the same network transaction lock;
new reserved legacy keys are fenced with `client_upgrade_required`. Legacy
orders deleted by the existing 30-day retention no longer provide this collision
evidence. Uncertain v1 rows must never migrate to jobs, including after retention.

`GET /jobs/by-key/{key}` reconciles the caller's original request key. Explicit
`?owner_id=...` is administrator-only. A lost response remains uncertain until
reconciled; 404 does not exclude another in-flight transaction. Do not replace
keys or fall back to per-entry submissions after uncertain acceptance.

The browser retains an explicitly recognized initial 400/403/429 rejection
as a locked immutable intent. Only that persisted definite rejection permits
user-confirmed resends of the same key and body while each preceding attempt
has a positively identified definite rejection; before each send it becomes
uncertain again. Unknown errors, conflicts, malformed acknowledgements,
transport failures and reconciliation 404 never grant this resend path.

## Controls And States

`POST /jobs/{job}/pause`, `/resume`, `/stop` accept only:

```json
{"operation_key":"operation-001","expected_control_version":"1"}
```

They return the successful summary, also on exact operation replay. Receipt
lookup precedes CAS; changed operation parameters or stale CAS return 409.
Only a currently authorized owner or administrator can control. Resume also
requires current original-owner craft authority and adopts its current epoch.
Permission loss durably pauses with `owner_permission_lost`; restoring permission
does not silently resume. Authority loss between polls remains fenced.

Pause stops future admission, not queued/executing orders. Stop is irreversible:
it cancels pending entries and linked queued orders, preserving assigned,
computing, running and needs_review work. It cannot cancel a native handle.
Settled/archived jobs cannot change control. No automatic retry, ownership
transfer, running cancel, force-finish or native-handle recovery is provided.

Summary fields are `id`, `network_id`, `owner_id`, `name`, `manifest_digest`,
`control`, `version`, `control_version`, `total`, `counts`, `created_at`,
`updated_at`, and optional `reason`, `result`, `settled_at`. Timestamps are RFC3339.
All versions/counts/total are decimal strings. Counters partition accepted entries:
pending, rejected, queued, assigned, computing, running, needs_review, done,
failed, canceled, expired. needs_review remains unsettled. Settlement results
are completed, canceled, or finished_with_issues. There is no cross-item
production percentage, material tree, ETA or output attribution.

## Synchronization

`GET /jobs` returns `{jobs,watermark,unsettled_count,settled_count,detailed_count,
receipt_count,entry_receipt_count,limits}` from one repeatable-read transaction.
`jobs` contains current unsettled summaries ordered by creation then ID.
Counts are network-wide; settled_count counts retained settled detail.

`GET /jobs/{job}` returns consistent detail. `GET /jobs/history?limit=100&cursor=...`
returns `{jobs,next_cursor,has_more}`, newest settlement then ID first.
The default/max limit is 100; each page is individually consistent. `next_cursor`
is opaque, network-bound, and empty at the end, not a persistent change cursor.

`GET /jobs/changes?after=0` returns
`{changes,next_after,has_more,watermark,reset}`. Each change is
`{revision,job_id,version,removed,summary?}`. Revisions and cursors are decimal
strings; rows ascend, with distinct revisions even within one transaction.
Summaries are historical values, never joins to mutable current detail.
Removal records have `removed:true` and omit summary.
Removal advances the entity version once beyond its last retained summary.

Drain at most 100 changes per response. Apply summaries and next_after together.
A nonempty page advances only to its last returned revision; an empty page keeps
the supplied after. Omitted/empty after means zero. Negative/nondecimal/overflow
cursors return 400; valid cursors below the retention floor or ahead of the
watermark return reset=true and no rows. On reset, replace the unsettled snapshot
and its watermark, invalidate cached history pages, then catch up again.

The existing SSE stream emits a coalesced `jobs` wake hint after commit. Its
event ID/revision is not a jobs cursor. Inventory revision, jobs watermark,
entity version and control_version are separate domains. Never advance jobs
cursor on an SSE hint. Pull/drain on open, reconnect, reset, visibility return
and a periodic fallback. Entry-level deltas are not provided.

## Budgets And Retention

The snapshot's integer `limits` map advertises these exact initial keys:

| Key | Value |
| --- | --- |
| entries_per_job / manifest_bytes | 500 / 262144 |
| unsettled_jobs_per_network / unsettled_jobs_per_owner | 64 / 16 |
| detailed_jobs_per_network | 512 |
| creation_receipts_per_network / source_entry_receipts_per_network | 16384 / 250000 |
| outstanding_entries_per_job | 8 |
| queued_job_orders_per_network | min(32, configured total queue limit) |
| entry_selections_per_poll / job_candidates_per_poll | 16 / 64 |
| retained_changes_per_network / changes_per_response | 2048 / 100 |
| control_operations_per_job / history_per_response | 1024 / 100 |
| settled_detail_retention_days | 30 |
| queued_orders_per_network / active_window / group_planning / response_bytes | configured existing budgets (defaults 256 / 4 / 1 / 32768) |

One control receipt slot is reserved for stop. Per-poll admission is quantum-one
cursor round-robin and creates orders only on valid primary polls, never
quantity-worker polls or maintenance. Existing total queue/execution/group
planning budgets still apply; saving intent does not reserve execution priority.
Offline compatible cached capability can accept intent; execution still waits
for a primary. Cached craftability is not a later recipe guarantee.

Keep all unsettled/review detail. Retain settled detail 30 days after settlement;
maintenance archives at most 64 jobs per pass, emitting removal tombstones.
Linked terminal orders remain until the job deadline, even if individually old.
Creation/source tombstones survive detail and action cleanup indefinitely within
their ceilings. Archived reconciliation/replay returns
`{"receipt":{"job_id":"...","manifest_digest":"...","archived":true}}`, without
editable detail, and never creates a new job.

## Validation And Deployment

Offline PostgreSQL/HTTP/simulator tests establish durable acceptance, admission,
identity, authorization, controls, synchronization and process restart semantics.
They do not establish real AE throughput, TPS, CPU targeting or native-handle
recovery. No deployment or live migration is implied by local validation.

Deployment requires explicit approval, a database backup and stopping the old
writer first. Run one backend writer only. Rollback after accepted jobs requires
a compatible backend or deliberate paired restore; do not silently orphan intent.
