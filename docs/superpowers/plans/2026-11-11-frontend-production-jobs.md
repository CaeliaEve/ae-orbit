# Frontend Production Jobs Implementation Plan

> Worker requirement: use subagent-driven-development and focused test-first implementation. No commits or pushes; retain the baseline and review evidence instead.

**Goal:** Adapt every new frontend submission to durable jobs and upgrade the approved local service without losing existing data.

**Architecture:** Keep the v1 legacy draft and order reader. Add a separate immutable job journal and validated jobs reader, integrate them into the existing task page, then verify and deploy one backend writer.

**Stack:** Existing browser ES modules, Node test runner, esbuild, Go/PostgreSQL, Playwright.

## Global Constraints

- Existing silver-white desktop/mobile design, item textures and four destinations remain.
- No real crafting, OC/game edits, credential changes, commits, pushes or unrelated refactors.
- CPU targeting remains disabled. Never discard a saved named CPU policy silently.
- No automatic mutation retry, new-key recovery, uncertain-intent unlock or fallback to individual orders.
- Preserve legacy uncertain orders and their original-key reconciliation.
- Use one implementation agent at a time and fresh context; independent review after each implementation task. The user subsequently requested gpt-6-astra for every newly spawned subagent; preserve already-running work.
- Snapshot dirty baseline files before editing. Use apply_patch for source edits.
- Deployment writes require escalation. Stop the old writer, verify an authoritative backup restore, then start one compatible writer. Never automatically restore an old DB after new acceptance.

## File Responsibilities

- `web/src/jobs.mjs`: immutable manifests, validated receipts, journal and exact string-version synchronization.
- `web/tests/jobs.test.mjs`: focused identity, persistence, acceptance and feed tests.
- `web/src/api.mjs`, `web/tests/api.test.mjs`: bounded conflict metadata and Retry-After, without retries.
- `web/src/app.mjs`: existing shell integration and legacy coexistence.
- Additional focused `web/src/job-*.mjs` only where they keep new controller/rendering responsibilities out of the already-large app.
- `web/static/index.html`, `web/src/style.css`: restrained jobs selector/list/detail/controls and responsive states.
- `contracts/production-jobs.md`, `README.md`: actual adopted behavior and usage.
- `.superpowers/sdd/2026-11-11-frontend-production-jobs/`: private baseline, briefs, reports, diffs and verification/deployment scripts.

## Task 1: Durable Client State And API

- [x] Write focused failing tests in `web/tests/jobs.test.mjs` for immutable ordered manifests, stable row-derived source keys, default CPU-only policy, member/network isolation, journal writes before POST eligibility, storage failure fail-closed, exact receipt/detail identity matching, archived receipt handling, source conflicts remaining locked, decimal-string versions, atomic cursor advancement, reset and tombstones. Add API tests for bounded job IDs and Retry-After.
- [x] Run `node --test web/tests/jobs.test.mjs web/tests/api.test.mjs` and record the expected missing-behavior failures.
- [x] Implement focused exported helpers in `jobs.mjs` and API metadata. Journal entries are individually keyed by request identity, not a shared replaceable index. Stable entry keys derive from the v1 draft row identity. Persist the immutable intent before claiming selected v1 rows with their existing submitting representation and a recognizable job batch prefix. Do not permit transport until both writes and reread verification succeed. Preserve original legacy request keys. New clients must recognize claims as jobs, never reconcile them via /orders. Restored in-flight records stay uncertain.
- [x] Receipt acceptance requires scoped original-key transport plus verified digest/id and complete requested entries, source keys, quantities and ordinals when detail exists. Archived receipts with no known prior digest are locked unless their authenticated original-key identity is sufficient under the backend contract; document this decision. Do not hand-roll SHA or assume crypto.subtle on LAN HTTP.
- [x] Snapshot/change parsing validates complete pages before applying any cursor; compare exact decimal strings with BigInt, retain tombstone version fences, invalidate history on reset. Control journal preserves action/key/version and allows explicit exact replay only.
- [x] Run focused tests and `npm run test:web`; record exports and integration examples in task report. Independent review must pass before Task 2.

## Task 1b: Retire Stale Browser Writers

- [x] Snapshot affected backend files against the current dirty baseline.
- [x] Focused TDD: existing exact legacy replay works; a NEW reserved `draft-` order returns `client_upgrade_required` without inserting; normal API keys still work; job source colliding with retained legacy order stays conflicted; race/concurrent paths cannot accept twice; owner/network scopes remain isolated.
- [x] In `internal/store/orders.go`, after exact replay and under existing network row lock, reject new reserved frontend draft keys with a typed error. Add HTTP error mapping. Do not alter unrelated legacy order keys or GET/review behavior.
- [x] In `internal/store/production_jobs.go`, before new acceptance under same lock, detect matching retained legacy order keys and return ambiguous conflict so frontend keeps intent locked. Do not fabricate job IDs for a legacy order. Exact existing-job replay remains first.
- [x] Run focused real-PG store/API tests serially. Document the reserved frontend namespace and 30-day legacy retention limitation. Independent review must pass before deployment.

## Task 2: Complete UI And Controller Adoption

- [x] Add focused failing orchestration tests for new single/batch submissions, old-backend fail-closed, lost/malformed acceptance, original-key 404, legacy uncertainty isolation, control replay and network generation changes. Use existing test conventions, no general framework.
- [x] Replace new `/orders` submission with one immutable `/jobs` manifest, including immediate single-item submissions. Probe authenticated snapshot for support. Permit offline OC intent acceptance but retain cached item capability checks. Keep drafts and journals member/network-local and refresh across tabs.
- [x] Make production tasks the default within the existing tasks destination; provide the individual-orders selector, unsettled/attention/history views, explicit history pagination, exact entry counts and details with item textures and existing linked-order review.
- [x] Implement authorized pause/resume/stop, honest stop confirmation, pending/uncertain control persistence and explicit same-operation replay. Preserve locked intent rows until verified acceptance. Show actionable unsupported, loading, empty, unauthorized and unavailable states.
- [x] Integrate snapshot and separate jobs changes cursor on SSE jobs hints/open/reconnect/reset, visibility, manual refresh and fallback. Guard all async state/detail updates by member/network generation and dialog identity.
- [x] Preserve styling and responsive dimensions. Update stale contract/docs statements about individual-only submissions. Run `npm run test:web` and `npm run build`, then independent task review.

## Task 3: Browser Verification And Approved Deployment

- [x] Reuse prior local browser harness patterns for fixture flows and isolated real-backend acceptance/restart. Block live business mutations. Capture desktop/mobile screenshots and check no overflow at widths 1440, 1024, 390, 320.
- [x] Verify old-backend detection, uncertain receipt recovery, conflicting source locks, controls, reset/reconnect, scoped switching and legacy uncertain isolation. Review all new frontend diff against baseline and fix findings through the responsible agent.
- [x] Build candidate Go executable with embedded assets, `-buildvcs=false`, workspace GOCACHE. Run focused API/contract checks serially against the dedicated test DB, not live.
- [x] Write a fail-closed deployment script: validate runtime PID/path/hash, schemas 001-012 and no active orders; preserve old binary/version; stop backend only; take plain SQL dump; create unique scratch DB, restore via psql and compare full table data digests; keep PG and preview running. No replacement if restore verification fails.
- [x] Replace executable and start existing runtime script in hidden fresh PowerShell. Verify ready, one PID/listener, candidate hash, assets, migration 013, unchanged credentials/business records. Authenticated GET-only smoke for networks/inventory/orders/jobs/history/changes. Never submit to live network.
- [x] Record version/provenance, backup evidence and rollback limits. Preserve private evidence because changes are not committed. Report actual deployed URL and limitations.

## Verification Commands

```powershell
npm run test:web
npm run build
$env:GOCACHE = Join-Path $PWD '.cache/go-build'
& 'E:/ae-courier/.tools/go/bin/go.exe' build -buildvcs=false -o '.superpowers/sdd/2026-11-11-frontend-production-jobs/ae-orbit.exe' ./cmd/ae-orbit
```

The live service must not be upgraded until frontend, isolated checks and independent review pass. No final completion claim without actual command output and deployed verification.
