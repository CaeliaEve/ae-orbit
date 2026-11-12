# Frontend Production Job Adoption

Date: 2026-11-11
Status: Approved by the user on 2026-11-11 (frontend adaptation and live upgrade).

## Intent

Fully adapt the approved silver-white desktop/mobile frontend to the verified
durable production-job backend. Preserve the existing visual language, item
textures, inventory browsing, CPU observations and legacy order review.

The user requested complete adaptation after backend acceptance and explicitly
approved upgrading the currently running local service with "升级 批准".
No commits, pushes, real crafting, credential changes or OC/game edits follow
from this design.

## Approach

Recommended: production jobs become the default task view and new submission
path. Keep individual orders in a separate selector within the same task view,
preserving the four existing desktop/mobile navigation destinations.

Alternative: mix job summaries and individual orders in one list. This avoids
an additional selector but mixes distinct control/status/retention models and
can display the same work at two levels. Prefer the separate views.

Retaining browser-driven per-entry submission would not adopt the new durable
acceptance guarantee, so it is not the chosen new-intent workflow.

## Submission And Local Safety

- Selected editable draft rows become one immutable job manifest; immediate
  single-item submission uses the same path with one entry.
- Keep unsubmitted drafts local. Accepted jobs, not editable drafts, synchronize
  between devices through the backend.
- Before POST, durably record member/network, request key, stable source keys,
  ordered item identities/quantities, name, normalized TTL and original draft
  associations. A storage error prevents sending the request.
- Use stable source identities across concurrent tabs. Reuse the existing
  member/network isolation and Web Locks boundary where available; never rely
  on tab timing alone to prevent duplicate source acceptance.
- Only a verified acceptance or original-key reconciliation clears associated
  editable draft rows. Partial acknowledgements, malformed success responses,
  ambiguous conflicts and transport failures retain a locked uncertain intent.
- A reconciliation 404 does not prove rejection. No automatic mutation retry,
  replacement keys, or fallback from uncertain job submission to legacy orders.
- Preserve v1 submitting/uncertain legacy entries and their original order-only
  reconciliation. Never silently convert them into a job.
- Named CPU policies remain unsupported. Do not silently discard a saved
  nondefault CPU choice, and never send CPU fields in job manifests.
- For mixed-version browser safety, new source keys reuse `draft-{row.id}`.
  After exact existing-order replay, the upgraded backend rejects NEW legacy
  order creation in the reserved `draft-` namespace with `client_upgrade_required`.
  Job acceptance checks for matching retained legacy order keys under the same
  network lock and leaves that source collision locked. This retires stale
  browser writers while preserving legacy reads, exact replay and non-browser
  order keys. It does not extend historical legacy-order retention.
- Permit offline-OC acceptance when the backend validates cached capability;
  distinguish saved pending intent from actual execution.

## Tasks, Details And Controls

- Default production-task view distinguishes unsettled, attention-requiring and
  retained settled jobs. History loads through explicit bounded pagination.
- Show names/identities, owner, creation time, admission control and exact entry
  state counts. Do not invent aggregate item percentages, ETA or native progress.
- Job detail displays authoritative entry states and resolved item textures.
  A linked order opens the existing order detail/review UI.
- Pause/resume/stop are available only to authorized actors and valid states.
  Stop confirmation states that queued/pending work is withdrawn but already
  executing or uncertain work remains. Permission loss never auto-resumes.
- Persist each uncertain control operation with its original operation key and
  control version. Refresh/reconcile before allowing a new action; never
  silently replace the key or retry a mutation in the background.
- Archived receipts acknowledge accepted history without presenting an editable
  manifest or permitting controls/readmission.
  An authenticated original-key GET can verify archived identity through the
  backend's immutable owner/network/key binding; compare any prior ID/digest
  and persist the receipt before clearing claims. A direct POST archived receipt
  without a known digest requires that GET before acceptance.

## Synchronization And Compatibility

- Probe the authenticated jobs snapshot to determine support. An authoritative
  missing route means unsupported; authorization/network failures remain errors.
  An old backend disables new job submission with a clear update-required state,
  while legacy reads/reconciliation remain available.
- Initialize from a consistent snapshot and its jobs watermark, then drain the
  dedicated changes feed. Apply each validated page and its cursor together.
- Pull on SSE jobs hints, stream open/reconnect, reset, visibility return,
  manual refresh and periodic fallback. SSE IDs never advance the jobs cursor.
- On reset, replace the unsettled snapshot/watermark, invalidate cached history
  and catch up again. Apply increasing entity versions and removal tombstones.
- Guard every request and dialog by member/network generation. Stale responses
  cannot replace a newer network, reopen a dismissed dialog or erase user input.
- Keep legacy orders.mjs and its bounded state-specific behavior intact.

## Modules And Verification

Use focused pure job intent/synchronization modules with existing API transport
and app orchestration. Preserve the v1 draft validator/state machine rather than
mixing its per-order semantics into immutable job intents. Extend API errors
with bounded conflict job IDs and Retry-After metadata without adding retries.

Follow focused test-first implementation and independent review. Verify unit
boundaries for identity persistence, uncertain outcomes, archived receipts,
atomic cursor application, resets, stale responses and control CAS. Exercise
real browser desktop/mobile flows, old-backend support detection, reconnect,
cross-tab safety and legacy uncertainty isolation. Use existing NEI assets and
inspect screenshots/overflow at 1440, 1024, 390 and 320 pixels.

Use an isolated real backend/simulator for end-to-end acceptance, never the
user's live crafting network. Reuse existing test/build tools; do not introduce
a general regression framework.

## Approved Deployment

Frontend implementation, isolated verification and live upgrade are in scope.
Before upgrading the running local service: first inspect
the actual runtime, take and verify a database backup, stop the old writer,
install one compatible new backend with the built frontend, then perform
read-only/authenticated smoke checks. Do not run old/new writers together.

Keep the live backend unchanged until the verification and backup gates pass.
Do not claim offline verification proves game TPS,
crafting throughput, CPU targeting or native-handle recovery.
