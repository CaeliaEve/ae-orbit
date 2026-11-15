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

## CPU selection and waiting

The primary OC advertises `cpu_selection=true` when it has both CPU discovery
and crafting support. A named target is accepted only when the latest CPU
snapshot contains exactly one matching name. Duplicate names are treated as
ambiguous; snapshot IDs are observation-local and are not persisted as target
identities.

`wait_for_cpu=false` sends the command to the OC immediately. The OC checks the
target immediately before recipe lookup and rejects a missing, busy, or
ambiguous CPU without calling the recipe or AE request path. `wait_for_cpu=true`
keeps the order queued on the service until the backend observes exactly one
idle match in a complete (not truncated) snapshot; the OC then repeats the same
check and may wait between snapshots.
Automatic orders omit both fields and retain the existing behavior. A target
policy is part of idempotency and the durable production-job manifest, so a
changed CPU name or waiting flag is a conflict rather than a silent rewrite.

The native AE request is still subject to the game/mod's normal execution
timing between the final observation and the request call. The bridge therefore
fails closed before submission wherever it can observe an invalid target, and
reports any uncertain native outcome through the existing order review state.
