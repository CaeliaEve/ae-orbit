import test from "node:test";
import assert from "node:assert/strict";
import { createAPI } from "../src/api.mjs";

test("mutations require CSRF and are never automatically retried", async () => {
  let calls = 0;
  const api = createAPI(async () => { calls++; throw new TypeError("offline"); });
  await assert.rejects(api.request("/api/v1/example", { method: "POST", body: {} }), /会话/);
  assert.equal(calls, 0);
  api.setCSRF("test-csrf");
  await assert.rejects(api.request("/api/v1/example", { method: "POST", body: {} }), e => e.uncertain);
  assert.equal(calls, 1);
});

test("JSON mutations use cookie auth, CSRF and exact string quantities", async () => {
  const api = createAPI(async (url, options) => {
    assert.equal(options.credentials, "same-origin");
    assert.equal(options.headers["X-CSRF-Token"], "test");
    assert.equal(options.headers.Authorization, undefined);
    assert.equal(JSON.parse(options.body).quantity, "9007199254740993");
    return new Response('{"id":"order-1"}', { headers: { "Content-Type": "application/json" } });
  });
  api.setCSRF("test");
  assert.equal((await api.request("/api/v1/example", { method: "POST", body: { quantity: "9007199254740993" } })).id, "order-1");
});

test("server errors are uncertain; explicit validation rejections are not", async () => {
  for (const status of [400, 401, 403, 409, 429, 500, 503]) {
    const api = createAPI(async () => new Response('{"error":{"code":"busy"}}', { status }));
    api.setCSRF("test");
    await assert.rejects(api.request("/api/v1/example", { method: "POST", body: {} }),
      e => e.status === status && e.uncertain === (status >= 500));
  }
});

test("ambiguous HTTP outcomes never become explicit submission rejections", async () => {
  for (const [status, code] of [[408, "timeout"], [425, "request_failed"], [499, "request_failed"], [409, "conflict"]]) {
    const api = createAPI(async () => new Response(JSON.stringify({ error: { code } }), { status }));
    api.setCSRF("test");
    await assert.rejects(api.request("/api/v1/example", { method: "POST", body: {} }),
      error => error.status === status && error.uncertain);
  }
});

test("unrecognized error bodies cannot unlock a submitted intent", async () => {
  for (const [status, body] of [[400, "<html>proxy error</html>"], [401, ""], [409, "{}"], [404, '{"error":{"message":"unknown"}}']]) {
    const api = createAPI(async () => new Response(body, { status }));
    api.setCSRF("test");
    await assert.rejects(api.request("/api/v1/example", { method: "POST", body: {} }),
      error => error.status === status && error.uncertain);
  }
});

test("source conflicts retain bounded authorized job IDs and Retry-After metadata without retry", async () => {
  let calls = 0;
  const api = createAPI(async () => {
    calls++;
    return new Response(JSON.stringify({ error: { code: "entry_already_accepted", job_ids: ["job-1", "../bad", "x".repeat(129), "job-1"] } }),
      { status: 409, headers: { "Retry-After": "1" } });
  });
  api.setCSRF("test");
  await assert.rejects(api.request("/jobs", { method: "POST", body: {} }), error => {
    assert.deepEqual(error.jobIds, ["job-1"]);
    assert.equal(error.retryAfter, 1);
    assert.equal(error.uncertain, true);
    return true;
  });
  assert.equal(calls, 1);
});
