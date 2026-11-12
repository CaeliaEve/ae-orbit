# OC lifecycle implementation plan

> Execute with focused TDD and independent review. Preserve unrelated dirty work.
> Source-only change; no commit, deployment, formal release, or live restart.

**Goal:** recover OC connection/process failures while making deliberate stop reliable.

**Architecture:** retain transport retries inside one bridge lifetime. A shared
OpenOS service owner supervises that lifetime with bounded restart policy and
cooperative stop. Startup retries retain boot identity and exact hello bytes.

**Stack:** OpenOS Lua 5.2+, existing Lupa test harness.

## Shared interfaces

`require('aeorbit.cancel')` exposes `new({now=fn,sleep=fn})`, `is_stop(error)`, and
`rethrow(error)`. A context exposes `request_stop(reason)`, `check()`, and
`sleep(seconds)`. `check` throws a structured `{code='interrupted',stop=true,
fatal=true}` error after a stop request. `rethrow` preserves structured stops and
normalizes the exact OpenOS `interrupted` error. Wait slices are at most 0.1s.

The runtime calls `bridge:start()` repeatedly only for structured transient
failures. The same runner must retain its startup boot identity and frozen hello.
Runtime injects cancellable `clock.sleep`, `clock.check`, and `deps.yield`.

## Task 1: cancellation boundary

Owner files: new `oc/lib/aeorbit/cancel.lua`, existing `client.lua`, `adapter.lua`,
`tracker.lua`, `journal.lua`, and focused cancellation/client tests.

- [x] Add failing tests: stop closes HTTP handle without retry/reclassification;
  interrupted craft retains intent/needs_review and never resubmits; adapter
  fallback cannot turn interruption into missing recipe; HTTP 500 retries.
- [x] Run focused tests on three Lua versions and record expected failures.
- [x] Implement context and propagate cancellation through protected boundaries.
  Journal failures remain fatal; do not interrupt between craft return and durable
  handle-state recording merely because a cooperative stop flag was set.
- [x] Run focused tests and existing tracker/client/adapter tests; independent review.

Example invariant:

```lua
local ok, err = pcall(c.post, c, '/bridge/v2/poll', {})
assert(not ok and require('aeorbit.cancel').is_stop(err))
assert(closed and attempts == 1)
```

## Task 2: startup retry identity

Owner files: `oc/lib/aeorbit/runner.lua`, `cooperative.lua`, startup tests.

- [x] Add failing tests for lost hello acknowledgements in both protocols; retain
  boot identity, exact body and old journal until welcome is validated.
- [x] Cache successful nonce and serialized hello before the first send, using
  the existing client send API. Validate welcome and only then transition state.
- [x] Preserve intentional stops through protected native-read/control boundaries.
- [x] Prove repeated `start` after transport failure does not create another boot
  identity, clear the journal early, or auto-resubmit crafts. Run existing runners.

Example invariant:

```lua
assert(not pcall(bridge.start, bridge))
bridge:start()
assert(nonce_requests == 1 and hello_bodies[1] == hello_bodies[2])
```

## Task 3: runtime and service

Owner files: new `runtime.lua`, `service.lua`, `oc/rc.d/ae-orbit.lua`; entrypoint,
installer source list, README, and lifecycle tests.

- [x] Add failing executable tests for deliberate stop, exclusive ownership,
  bounded crash retries, fatal stop, asynchronous rc start, and cleanup.
- [x] Extract one bridge lifetime from entrypoint, retain acquisition pacing,
  add setup/startup protection, interrupt handling, redacted status, and cleanup.
- [x] Implement shared process-lifetime owner and detached service thread. Default
  crash policy allows three retries in ten minutes, capped 30s backoff; keep owner
  while stopping. `restart` waits up to five seconds, otherwise reports stopping.
- [x] Expose foreground `ae-orbit [config]` and explicit `start`, `stop`, `status`,
  `restart`; rc enable/disable uses native rc configuration preservation.
- [x] Include new files in transactional installer source manifest; never auto-enable
  or restart the running service during install. Formal package remains deferred.
- [x] Run `E:/ae-courier/.tools/python/Scripts/python.exe scripts/test_lua.py` and
  `git diff --check`; independently review the whole lifecycle change.

## Progress

- Design approved; baseline is dirty and intentionally preserved.
- Tasks 1 and 2 completed with focused RED/GREEN and independent safety review.
- Task 3 completed with unit, installer, native-source integration, and independent
  OpenOS review. Source distribution lists include the new modules and rc entry;
  no formal binary/ZIP was produced or deployed.
- Review fixes: cooperative zero-duration yield, HTTP close return failures,
  invalid client numeric settings, complete remote batch validation before side
  effects, healthy status reasons, late-stop status, and immediate rc startup errors.
- Final verification: 129 Lua executions and 96 installer tests passed; installed
  native OpenOS integration passed on Lua 5.2/5.3/5.4; `go test ./oc` passed;
  in-memory package recipe includes service files; `git diff --check` clean.
- Game acceptance remains pending: idle-safe deployment, reboot/autostart,
  temporary disconnection/recovery, Ctrl+C, manual stop, and no duplicate owner.
