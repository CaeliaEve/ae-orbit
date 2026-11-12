# OC lifecycle and recovery

Approved in chat on 2026-11-11. Implementation is source-only: no live OC restart,
deployment, formal release, backend asset refresh, commits, or credential changes.

## Behavior

- Use an OpenOS rc service, without modifying OpenOS itself. Enable/disable controls
  boot autostart; manual stop lasts until an explicit start or the next boot.
- Foreground Ctrl+C requests a cooperative stop. Background instances ignore shell
  interrupts and accept only their service stop request. Never kill a live thread
  as normal shutdown, or start its replacement before it has actually returned.
- Startup transport errors retry with capped exponential backoff and jitter. Keep
  one boot identity and byte-identical hello after an unacknowledged hello request.
- Established transport errors keep the runner, session, frozen requests, and
  crafting handles. HTTP 500 joins 429/502/503/504 as retryable server failures.
- Unexpected runtime errors have a small rolling restart budget. Configuration,
  authentication, protocol, stale session, journal, and cleanup errors stop for
  attention. Never obtain a fresh session just to cure stale_session.
- All starts share a local single-instance owner. Deliberate stop clears desired
  running before requesting cancellation. A stopping owner remains exclusive.
- Structured stop reasons survive nested protected calls. Close HTTP handles and
  unregister only owned listeners/subscriptions. Do not cancel native AE crafting,
  erase a journal, or resubmit an uncertain order as part of shutdown/recovery.
- Status exposes lifecycle state, restart count, and bounded redacted error codes.
  Persist a small last-state snapshot, not unbounded log lines or credentials.
- Same-OC recovery does not cover power loss, unloaded chunks, or a frozen VM.

## Components

1. `aeorbit.cancel`: structured intentional-stop identity, propagation helpers,
   and cooperative stop context with interruptible waits.
2. Existing HTTP/adapter/tracker/runner modules: cancellation preservation and
   safe startup retry. Execution journals retain fail-closed semantics.
3. `aeorbit.runtime`: one bridge lifetime, resource setup/cleanup, status updates.
4. `aeorbit.service`: shared owner, foreground/background control, recovery policy.
5. `oc/rc.d/ae-orbit.lua`: thin OpenOS rc integration; `oc/bin/ae-orbit.lua` remains
   the foreground command and gains status/stop/start/restart commands.

## Verification

Focused real-Lua behavior tests cover stop during network/startup/backoff, lost
hello acknowledgement, frozen replay, interrupted crafting uncertainty, resource
cleanup, manual stop without revival, duplicate starts, crash budget, and blocked
failures. Run existing Lua and installer tests on Lua 5.2/5.3/5.4. Keep native-game
acceptance explicitly pending; do not claim isolated tests prove live recovery.
