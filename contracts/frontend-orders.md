# Frontend order safety

The quantity is an additional crafting amount, not a target inventory level.
Browser drafts are device-local. Submitted orders and their optional `batch_id`
are stored on the service and can be viewed from either device.

## Batch submission and uncertain transport

Each draft entry has its own `idempotency_key`. A batch is a display grouping,
not an atomic transaction. `batch_id` is persisted and is part of the exact
parameter comparison for an idempotent replay; accepted entries must not be
submitted again as new intents.

After a submission loses its response, the browser may use
`GET /api/v1/networks/{network}/orders/by-key/{key}`. This endpoint only reads the
current member's retained order in that network. A missing result is not proof
that an earlier in-flight request will never finish. It must not automatically
trigger a second POST or enable editing and resubmission of that uncertain
intent. Existing retention limits still apply.

The local request key derives from the persisted entry ID. Concurrent tabs
without Web Locks can race with different display batches, but still use the
same request key. Read-only reconciliation verifies the member, network,
canonical item and quantity, then adopts the server's authoritative batch.

New `/orders` requests with reserved `draft-` keys return HTTP 409
`client_upgrade_required`; retained exact order replays still succeed and changed
replays still conflict. This error must remain unknown to the old bundled client
so its v1 row stays uncertain, never editable or migrated automatically to a job.
Legacy reads, reviews and by-key reconciliation remain available.

## Bounded task lists and live details

The browser reads active orders by exact state (queued, assigned, computing,
running), so newer completed records cannot evict them from the active view.
Attention and ended views also use state-specific reads. Each state is limited
to its latest 200 records; a full page is labeled as bounded and the active
count is marked as a lower bound. The all view shows only the latest 200 records,
not unlimited history. These reads do not provide an atomic cross-state snapshot.

An open task detail is refreshed on order events, periodic reads and manual
refresh. Closing it or changing networks invalidates pending detail responses.
An unfinished administrator review form stays connected while the same order
still needs review; obsolete actions disappear after an authoritative read.
No automatic refresh retries a mutation or changes an uncertain draft.

## CPU selection is not yet safe on the installed bridge

The installed `OpenComputers-1.12.68-GTNH.jar` was inspected with `javap`.
Its `NetworkControl$Craftable.request` accepts a CPU name but resolves an
unmatched name to `null`, allowing AE to choose automatically. The separate
`getCpus` call is not an atomic guard against that fallback. Duplicate CPU names
are also not a stable identity. CPU snapshot IDs are observation-local.

The current adapter therefore reports `cpu_selection=false`. The service
rejects a hello advertising `true`, rejects nonempty `cpu_name` or
`wait_for_cpu=true` before enqueueing, and the Lua adapter independently rejects
targeted commands before recipe lookup or `request`. Automatic orders remain
unchanged. A strict CPU selector, busy-CPU waiting, and assignment migration are
not implemented or advertised.

Enabling this capability requires a proven atomic native operation that rejects
missing or ambiguous targets without choosing another CPU, plus a new verified
execution/waiting contract. A dropdown and preflight observation alone are not
sufficient. No game mod was modified as part of this frontend work.
