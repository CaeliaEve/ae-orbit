-- The installer creates /etc/ae-orbit.cfg if absent. Set these connection values.
return {
  bridge_mode = "single",
  role = "primary",
  url = "https://ae.example.com",
  token = "replace-with-the-device-token-issued-by-the-backend",
  me_address = "replace-with-the-ME-controller-or-full-interface-component-address",
  state_dir = "/var/lib/ae-orbit",

  -- Budgets are further reduced according to free memory and server limits.
  max_batch_bytes = 16384,
  max_response_bytes = 16384,
  cache_entries = 256,
  dirty_entries = 128,
  identity_bytes = 4096,
  scan_slice_seconds = 0.05,
  -- Aggregate ordinary observations across slices; priority confirmations flush immediately.
  batch_interval_seconds = 0.25,
  scan_interval = 60,
  cpu_interval = 10,
  cpu_limit = 16,
  http_timeout = 10,

  -- Optional event source addresses. OC compound drivers can emit from an
  -- internal node, so do not assume it equals me_address. Every hint is reread
  -- against the configured ME component before a quantity is uploaded.
  -- event_sources = {"verified-source-node-address"},
}
