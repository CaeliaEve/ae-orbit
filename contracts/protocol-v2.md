# Cooperative Bridge Protocol v2

Protocol 2 is available only at `/bridge/v2/*` on networks explicitly enabled
through `POST /api/v1/networks/{network}/cooperation` with `{}`. Single mode
retains protocol 1. Neither a worker nor a cooperative network may use v1,
including its boot-ID endpoint. There is no hot downgrade operation.

## Identity and Sessions

Each device has an independent token, boot, session, inventory sequence and
control sequence. Hello uses `protocol:2`; Welcome returns server-determined
`device_id` and `role` (`primary` or `quantity_worker`). Client claims cannot
grant a role. A live boot can resume; a retired boot returns 409. Rotation,
revocation or mode changes invalidate affected sessions and read ownership.

Create workers through `/api/v1/networks/{network}/devices`; list access requires
network viewing permission. Creation, token rotation, revocation and cooperation
activation require admin permission and browser Origin/CSRF protection. Tokens
are returned only on creation/rotation, never in device lists.

## Bounded Reads

Poll declares `read_slots` (0..16) and `read_bytes` (0..16384). Both must be
positive for new reads; zero capacity does not prevent heartbeats or ACKs.
Legacy `recheck_*` and `refresh_*` capacities are rejected. A primary may include
at most sixteen `hints`, each containing exactly one decimal-string `id` or
complete `item` definition and no quantity. Hints are not inventory evidence.

PollAck dispatches at most sixteen `reads`, each with canonical decimal-string
`id`, complete `item`, random 32-lowercase-hex `assignment_id`, `kind`
(`quantity` or `quantity_recipe`), `purpose` (`refresh`, `order`, `background`),
and remaining `ttl_millis` (1..30000). At most four explicit refresh reads are
included. Per-device outstanding reads are limited to sixteen; per-network to
128. Joint quantity/recipe work belongs only to the primary.

Acquisition `scan_budget` is attached to `grant_id` and `grant_ttl_millis`.
Credits are shared across devices and networks in a server group, including v1.
A new grant replaces the prior grant; it does not add unused credits. Repeated
grant IDs do not replenish credits or reset their expiry. Subtract the HTTP
round-trip wall-clock duration before using any grant; ignore an expired grant.
Retries do not renew read leases or acquisition grants.

## Inventory Receipts

Update retains explicit decimal-string `quantity` and `observation`, explicit
`craftable` and exactly one `id`/`item`. A task receipt includes
`assignment_id`; legacy `verification` and `refresh` receipts are rejected.
`quantity` requires `craftable:null`; `quantity_recipe` requires a boolean.
Workers can only return assigned quantities for known canonical IDs. They
cannot submit definitions, discovery hints, scan completion, CPU snapshots,
recipe evidence, order reports or craft commands.

Sequence, immutable-body replay, short-ID bindings, field acceptance and
transport ACK are committed atomically. Decimal strings must never pass through
floating point. Expired, revoked or superseded but otherwise valid receipts may
receive transport ACK without modifying quantities, recipe evidence, observation
times or refresh completion. Cross-network IDs, invalid structures and forged
permissions are rejected. Replayed PollAck filters lost ownership and never
mints replacement reads or grants.

Only the primary discovers identities and maintains metadata. Discovery can
initialize genuinely missing field evidence once. Existing canonical entries,
including aliases or alternate NBT compression, require current field ownership.
Quantity and recipe sources are independent; worker quantities never refresh
recipe evidence or complete order verification. Logical refresh IDs and original
deadlines survive reassignment; reads already in flight before the request do
not retroactively confirm it.

## Query Status

Network `session_id`, `online`, capabilities and CPU state continue to describe
the primary. `services` exposes online quantity-reader count and independent
discovery, recipe and craft availability. A worker heartbeat cannot restore a
primary capability. Item `current_session` checks its quantity source device;
`craftable_current_session` independently checks its recipe source. Observation
timestamps remain historical acceptance times, never promises of freshness.
