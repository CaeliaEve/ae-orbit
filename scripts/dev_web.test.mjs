import assert from "node:assert/strict";
import { mkdtemp, mkdir, writeFile, rm } from "node:fs/promises";
import http from "node:http";
import { tmpdir } from "node:os";
import path from "node:path";
import { once } from "node:events";
import test from "node:test";
import { createDevServer, startDevServer } from "./dev_web.mjs";

const hash = "a".repeat(64);

async function fixture(t) {
  const root = await mkdtemp(path.join(tmpdir(), "ae-orbit-dev-"));
  await mkdir(path.join(root, "assets/nei"), { recursive: true });
  await mkdir(path.join(root, "assets/brand"), { recursive: true });
  await Promise.all([
    writeFile(path.join(root, "index.html"), "<!doctype html><title>AE Orbit</title>"),
    writeFile(path.join(root, "app.js"), "export const app = true;"),
    writeFile(path.join(root, "style.css"), "body{color:black}"),
    writeFile(path.join(root, "compact.css"), "html[data-inventory-layout=\\\"dense\\\"]{}"),
    writeFile(path.join(root, "assets/nei/manifest.json"), '{"version":1,"icons":{}}'),
    writeFile(path.join(root, `assets/nei/${hash}.png`), Buffer.from([137, 80, 78, 71])),
    writeFile(path.join(root, "assets/brand/ae-orbit-mark.png"), Buffer.from([137, 80, 78, 71])),
    writeFile(path.join(root, "assets/brand/ae-orbit-mark.svg"), "<svg></svg>"),
    writeFile(path.join(root, "assets/nei/audit.json"), '{"private":"audit"}'),
    writeFile(path.join(root, "credentials.txt"), "never serve this"),
  ]);
  t.after(() => rm(root, { recursive: true, force: true }));
  return root;
}

async function listen(t, server) {
  server.listen(0, "127.0.0.1");
  await once(server, "listening");
  t.after(() => close(server));
  return `http://127.0.0.1:${server.address().port}`;
}

function close(server) {
  return new Promise((resolve) => {
    server.closeAllConnections();
    server.close(resolve);
  });
}

function request(url, { method = "GET", headers = {}, body } = {}) {
  return new Promise((resolve, reject) => {
    const req = http.request(url, { method, headers }, (res) => {
      const chunks = [];
      res.on("data", (chunk) => chunks.push(chunk));
      res.on("end", () => resolve({
        status: res.statusCode,
        headers: res.headers,
        text: Buffer.concat(chunks).toString(),
      }));
    });
    req.on("error", reject);
    req.end(body);
  });
}

test("imports without listening and serves only the static allowlist", async (t) => {
  const root = await fixture(t);
  const server = createDevServer({ root });
  assert.equal(server.listening, false);
  const origin = await listen(t, server);
  const page = await request(`${origin}/`);
  assert.equal(page.status, 200);
  assert.match(page.text, /AE Orbit/);
  assert.match(page.headers["content-security-policy"], /connect-src 'self'/);
  assert.equal(page.headers["x-content-type-options"], "nosniff");
  assert.match(page.headers["cache-control"], /no-store/);
  assert.match((await request(`${origin}/app.js`)).headers["content-type"], /javascript/);
  assert.match((await request(`${origin}/style.css`)).headers["content-type"], /css/);
  const compact = await request(`${origin}/compact.css`);
  assert.equal(compact.status, 200);
  assert.match(compact.headers["content-type"], /css/);
  assert.match(compact.text, /data-inventory-layout/);
  assert.equal((await request(`${origin}/assets/nei/manifest.json`)).status, 200);
  const image = await request(`${origin}/assets/nei/${hash}.png`);
  assert.equal(image.status, 200);
  assert.match(image.headers["cache-control"], /immutable/);
  const brand = await request(`${origin}/assets/brand/ae-orbit-mark.png`);
  assert.equal(brand.status, 200);
  assert.match(brand.headers["content-type"], /image\/png/);
  assert.match(brand.headers["cache-control"], /immutable/);
  const brandVector = await request(`${origin}/assets/brand/ae-orbit-mark.svg`);
  assert.equal(brandVector.status, 200);
  assert.match(brandVector.headers["content-type"], /image\/svg\+xml/);
  assert.match(brandVector.headers["cache-control"], /immutable/);
  for (const name of ["/assets/nei/audit.json", "/credentials.txt", "/app.js.map",
    "/assets/nei/not-a-hash.png", "/assets/brand/secret.png", "/scripts/dev_web.mjs", "/compact.css.map", "/assets/nei/"]) {
    assert.equal((await request(origin + name)).status, 404, name);
  }
  const head = await request(`${origin}/app.js`, { method: "HEAD" });
  assert.equal(head.status, 200);
  assert.equal(head.text, "");
  assert.equal((await request(origin, { method: "POST" })).status, 405);
});

test("API access without a configured backend returns an explicit failure", async (t) => {
  const origin = await listen(t, createDevServer({ root: await fixture(t) }));
  const response = await request(`${origin}/api/v1/networks`);
  assert.equal(response.status, 503);
  assert.equal(JSON.parse(response.text).error.code, "dev_backend_unavailable");
  assert.equal(response.headers["access-control-allow-origin"], undefined);
});

test("backend targets are constrained to loopback origins", () => {
  for (const backend of ["http://example.com", "http://127.0.0.1.example.com",
    "file:///tmp/test", "http://user:pass@127.0.0.1", "http://127.0.0.1/api",
    "http://127.0.0.1?target=x", "http://127.0.0.1#target"]) {
    assert.throws(() => createDevServer({ backend }), /loopback|origin/i, backend);
  }
  for (const backend of ["http://127.0.0.1:8080", "http://localhost:8080", "https://[::1]:8080"]) {
    assert.equal(createDevServer({ backend }).listening, false);
  }
});

test("proxy mutations require same dev Origin before any upstream request", async (t) => {
  const received = [];
  const backend = await listen(t, http.createServer((req, res) => {
    received.push({ url: req.url, headers: req.headers });
    req.resume();
    res.setHeader("Set-Cookie", "ac_session=test; Path=/api/; HttpOnly; SameSite=Strict");
    res.setHeader("Access-Control-Allow-Origin", "*");
    res.end('{"accepted":true}');
  }));
  const origin = await listen(t, createDevServer({ root: await fixture(t), backend }));
  for (const candidate of [undefined, "null", "https://other.example", `${origin}/`]) {
    const response = await request(`${origin}/api/v1/session`, {
      method: "POST", headers: candidate ? { Origin: candidate } : {},
    });
    assert.equal(response.status, 403);
  }
  assert.equal(received.length, 0);
  const response = await request(`${origin}/api/v1/session`, {
    method: "POST",
    headers: { Origin: origin, "Content-Type": "application/json", "X-CSRF-Token": "csrf",
      Cookie: "ac_session=existing", "X-Forwarded-Host": "untrusted.example" },
    body: "{}",
  });
  assert.equal(response.status, 200);
  assert.equal(received.length, 1);
  assert.equal(received[0].headers.origin, backend);
  assert.equal(received[0].headers.host, new URL(backend).host);
  assert.equal(received[0].headers["x-csrf-token"], "csrf");
  assert.equal(received[0].headers.cookie, "ac_session=existing");
  assert.equal(received[0].headers["x-forwarded-host"], undefined);
  assert.deepEqual(response.headers["set-cookie"],
    ["ac_session=test; Path=/api/; HttpOnly; SameSite=Strict"]);
  assert.equal(response.headers["access-control-allow-origin"], undefined);
});

test("foreign Host and cross-origin reads cannot use the proxy", async (t) => {
  let requests = 0;
  const backend = await listen(t, http.createServer((req, res) => {
    requests += 1;
    res.end("{}");
  }));
  const origin = await listen(t, createDevServer({ root: await fixture(t), backend }));
  assert.equal((await request(`${origin}/api/v1/me`, {
    headers: { Host: `untrusted.example:${new URL(origin).port}` },
  })).status, 403);
  assert.equal((await request(`${origin}/api/v1/me`, {
    headers: { Origin: "https://untrusted.example" },
  })).status, 403);
  assert.equal((await request(`${origin}/api/v1/me`, { method: "OPTIONS" })).status, 405);
  assert.equal(requests, 0);
});

test("SSE is streamed immediately and abort closes the upstream response", async (t) => {
  let disconnected;
  const closed = new Promise((resolve) => { disconnected = resolve; });
  const backend = await listen(t, http.createServer((req, res) => {
    res.writeHead(200, { "Content-Type": "text/event-stream", "Cache-Control": "no-cache" });
    res.write("event: inventory\ndata: {\"revision\":\"2\"}\n\n");
    res.on("close", disconnected);
  }));
  const origin = await listen(t, createDevServer({ root: await fixture(t), backend }));
  const abort = new AbortController();
  const response = await fetch(`${origin}/api/v1/events`, { signal: abort.signal });
  assert.match(response.headers.get("content-type"), /text\/event-stream/);
  const reader = response.body.getReader();
  const first = await reader.read();
  assert.match(new TextDecoder().decode(first.value), /revision.*2/);
  abort.abort();
  await closed;
});

test("upstream failures are not retried, including POST", async (t) => {
  let requests = 0;
  const backend = await listen(t, http.createServer((req) => {
    requests += 1;
    req.socket.destroy();
  }));
  const origin = await listen(t, createDevServer({ root: await fixture(t), backend }));
  const response = await request(`${origin}/api/v1/orders`, {
    method: "POST", headers: { Origin: origin }, body: "{}",
  });
  assert.equal(response.status, 502);
  assert.equal(requests, 1);
});

test("an occupied preferred port falls back to an available loopback port", async (t) => {
  const occupied = http.createServer();
  await listen(t, occupied);
  const preferred = occupied.address().port;
  const server = await startDevServer({ root: await fixture(t), port: preferred });
  t.after(() => close(server));
  assert.notEqual(server.address().port, preferred);
  assert.equal(server.address().address, "127.0.0.1");
  assert.equal((await request(`http://127.0.0.1:${server.address().port}/`)).status, 200);
});
