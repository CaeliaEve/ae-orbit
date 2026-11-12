# Frontend Task Continuity Implementation Plan

> For agent workers: use subagent-driven-development for the independent order
> reader and focused review. Keep the existing dirty worktree; do not commit,
> deploy a backend, or submit real crafting orders.

**Goal:** Keep active tasks discoverable during high-volume ordering, keep an
open task's status current, and expose connection state on mobile.

**Architecture:** Reuse the existing exact-state order GET endpoint without
changing its response, retention, or 200-row limit. Combine state-specific reads
in a small frontend module. The app owns request freshness, dialog lifecycle,
and preservation of an administrator's unfinished review form.

**Stack:** Existing JavaScript modules, Lucide, Node's test runner, esbuild, and
the locally available Playwright/Chromium runtime.

## Agreed Scope And Alternatives

Continue the user's approved silver-white, crafting-focused desktop/mobile
interface and high-volume ordering workflow.

- Chosen: state-specific reads, bounded results labeled explicitly, and live
  read-only detail refreshes. This fixes the observed gaps using the installed
  backend.
- Rejected for this iteration: backend cursor pagination. It is a separate API
  contract and deployment, unnecessary for preventing completed orders from
  evicting active states.
- Rejected: increasing the unfiltered limit or caching all history in the
  browser. Neither ensures active-state visibility or bounds retained history.

Preserve idempotency, uncertain-result locking, CSRF, and the strict CPU
capability gate. Never infer a completion percentage from task state.

## Task 1: Read Orders By State

Files:
- Add `web/src/orders.mjs`.
- Add `web/tests/orders.test.mjs`.
- Integrate into `web/src/app.mjs`.

- [x] Write tests for 200 newer completed records plus older running/review
  tasks; independent active counts while viewing ended tasks; duplicate IDs;
  per-state truncation; rejected/invalid responses.
- [x] Run `node --test web/tests/orders.test.mjs` and observe failure.
- [x] Implement the following production interface:

```js
loadOrderSnapshot(fetchOrders, filter = "active")
// fetchOrders({ state, limit: "200" }) -> Promise<Order[]>
// -> { orders, active_count, active_truncated, limited_states, recent_only }
```

Fetch active states once per refresh for navigation counts; fetch additional
selected states once each. Filter sets are active (queued, assigned, computing,
running), attention (failed, needs_review), done (done, failed, canceled,
expired), and all (empty state). Deduplicate by ID, prefer a newer updated_at,
and sort by created_at descending then ID ascending.

- [x] Integrate with a request generation/abort guard so a slow prior filter
  response cannot replace a newer selection. Refresh filters through the server.
- [x] Show a lower-bound active count and explicit selected-state truncation.
- [x] Re-run the unit and browser scenarios.

## Task 2: Keep An Open Detail Current

Files:
- Modify `web/src/app.mjs`.
- Modify `web/static/index.html`.
- Exercise `.local/queue-20261111/regression.cjs`.

- [x] Reproduce stale queued details after an orders event, stale history, and a
  delayed response reopening a dismissed task.
- [x] Track the selected detail ID and invalidate in-flight reads on close,
  network switch, or selection change.
- [x] Open a loading dialog immediately; refresh its facts/history from existing
  GET endpoints during event/periodic/manual refreshes. Never reopen it from a
  background response.
- [x] Preserve the actual review form and focused field when the same order
  still needs review. Remove obsolete actions when state changes.
- [x] Provide an inline read error and a refresh icon. No mutation retries.
- [x] Re-run dialog scenarios, including retained review text and a close race.

## Task 3: Mobile Status And Verification

Files:
- Modify `web/src/style.css`.
- Modify connection accessibility markup in `web/static/index.html`.
- Update the bounded-order description in `contracts/frontend-orders.md`.

- [x] Reproduce the hidden mobile connection label on the task view.
- [x] Keep the connection label visible in a compact second header row and
  distinguish event-stream reconnecting from OC offline.
- [x] Verify 1440, 1024, 390, and 320px layouts with nonempty task fixtures,
  current item textures, and screenshots; assert no horizontal overflow.
- [x] Run `npm run build`, `node --test web/tests/*.test.mjs
  scripts/dev_web.test.mjs`, the focused browser scenarios, and `git diff --check`.
- [x] Run a real-backend browser smoke with business mutations blocked. Preserve
  the user's live session and drafts by using an isolated browser context.
- [x] Leave the existing preview on port 4317 running. Report its actual result
  and outstanding limits; do not claim real crafting acceptance.

## Evidence

- The first browser run failed all five original scenarios for the expected
  missing behavior. A sixth scenario reproduced the 200-item lookup boundary.
  Red output is retained at `.local/queue-20261111/regression-red.json`.
- The independent order-reader review passed specification and quality checks.
- Root verification: all 37 frontend/proxy unit tests and all eight new browser
  scenarios passed. The two existing offline-submission scenarios and two
  inventory/CPU recovery scenarios passed. The build and diff check passed.
- The integration review passed specification and quality checks. Two additional
  browser scenarios verified bounded-history labeling and list/detail read
  failures without losing review input.
- Desktop and mobile screenshots were visually inspected. Connection status
  remains visible and no document overflow was observed at all four widths.
- Live smoke exited 0 against the actual served build: every filter returned
  HTTP 200, two retained done orders were readable, one detail and its four
  history entries loaded and manually refreshed, and the closed dialog remained
  closed through a 15-second observation. There were no business write attempts,
  runtime errors, console errors or HTTP failures. The isolated session was
  created with 201 and revoked with 204. The report is
  `.local/queue-20261111/live-smoke-results.json`.
- Actual current state: both OCs were offline; CPU observations were retained
  snapshots. No fresh online heartbeat, changing live order, or real crafting
  acceptance is claimed. Network-switch and replacement-detail guards were
  reviewed statically; the browser regression exercises close/filter races.
- Root confirmed served HTML, JavaScript and CSS exactly match the local build.
  Preview PID 32932 remains listening on 4317; backend PID 22600 remains listening
  on 18080. No backend was rebuilt or deployed, so its embedded frontend is
  unchanged. No commit, credential rotation, OC installation or save edit occurred.
