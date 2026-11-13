import http from "node:http";
import https from "node:https";
import { createReadStream } from "node:fs";
import { realpath, stat } from "node:fs/promises";
import path from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";

const defaultRoot = fileURLToPath(new URL("../web/static/", import.meta.url));
const csp = "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; "
  + "connect-src 'self'; object-src 'none'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'";
const security = {
  "Content-Security-Policy": csp,
  "X-Content-Type-Options": "nosniff",
  "Referrer-Policy": "same-origin",
  "Cross-Origin-Resource-Policy": "same-origin",
};
const hopByHop = new Set([
  "connection", "keep-alive", "proxy-authenticate", "proxy-authorization",
  "te", "trailer", "transfer-encoding", "upgrade",
]);
const files = new Map([
  ["/", ["index.html", "text/html; charset=utf-8"]],
  ["/index.html", ["index.html", "text/html; charset=utf-8"]],
  ["/app.js", ["app.js", "text/javascript; charset=utf-8"]],
  ["/style.css", ["style.css", "text/css; charset=utf-8"]],
  ["/compact.css", ["compact.css", "text/css; charset=utf-8"]],
  ["/assets/nei/manifest.json", ["assets/nei/manifest.json", "application/json; charset=utf-8"]],
  ["/assets/brand/ae-orbit-mark.png", ["assets/brand/ae-orbit-mark.png", "image/png"]],
  ["/assets/brand/ae-orbit-mark-dark.png", ["assets/brand/ae-orbit-mark-dark.png", "image/png"]],
  ["/assets/brand/favicon.png", ["assets/brand/favicon.png", "image/png"]],
  ["/assets/brand/ae-orbit-mark.svg", ["assets/brand/ae-orbit-mark.svg", "image/svg+xml"]],
]);

function backendOrigin(value) {
  if (!value) return null;
  let url;
  try { url = new URL(value); } catch { throw new Error("Backend must be a loopback HTTP(S) origin."); }
  const host = url.hostname;
  const loopback = host === "localhost" || host === "[::1]"
    || /^127(?:\.\d{1,3}){3}$/.test(host);
  if (!["http:", "https:"].includes(url.protocol) || !loopback
      || url.username || url.password || url.pathname !== "/" || url.search || url.hash) {
    throw new Error("Backend must be a loopback HTTP(S) origin without credentials or a path.");
  }
  return url;
}

function localOrigin(req) {
  const host = req.headers.host;
  if (typeof host !== "string") return null;
  const port = req.socket.localPort;
  if (![ `127.0.0.1:${port}`, `localhost:${port}`, `[::1]:${port}` ].includes(host)) return null;
  return `http://${host}`;
}

function jsonError(res, status, code) {
  if (res.destroyed) return;
  res.writeHead(status, {
    ...security, "Content-Type": "application/json; charset=utf-8", "Cache-Control": "no-store",
  });
  res.end(JSON.stringify({ error: { code } }));
}

function cleanHeaders(headers, { response = false } = {}) {
  const blocked = new Set(hopByHop);
  for (const token of String(headers.connection || "").split(",")) blocked.add(token.trim().toLowerCase());
  const result = {};
  for (const [name, value] of Object.entries(headers)) {
    const lower = name.toLowerCase();
    if (blocked.has(lower) || lower === "forwarded" || lower.startsWith("x-forwarded-")
        || lower === "x-real-ip" || lower === "x-original-url" || lower === "x-rewrite-url"
        || response && lower.startsWith("access-control-")) continue;
    if (value !== undefined) result[name] = value;
  }
  return result;
}

function proxy(req, res, backend, origin) {
  if (req.method === "OPTIONS" || req.method === "CONNECT" || req.method === "TRACE") {
    jsonError(res, 405, "method_not_allowed");
    return;
  }
  const mutation = !["GET", "HEAD"].includes(req.method);
  if ((mutation || req.headers.origin !== undefined) && req.headers.origin !== origin) {
    jsonError(res, 403, "forbidden");
    return;
  }
  if (!backend) {
    jsonError(res, 503, "dev_backend_unavailable");
    return;
  }
  const target = new URL(req.url, backend);
  if (target.origin !== backend.origin || !target.pathname.startsWith("/api/")) {
    jsonError(res, 400, "invalid_request");
    return;
  }
  const headers = cleanHeaders(req.headers);
  headers.host = backend.host;
  if (req.headers.origin !== undefined) headers.origin = backend.origin;
  const transport = backend.protocol === "https:" ? https : http;
  // Each request gets one upstream attempt. In particular, an interrupted POST
  // must remain uncertain rather than being transparently submitted again.
  const upstream = transport.request(target, { method: req.method, headers, agent: false }, (response) => {
    const outgoing = cleanHeaders(response.headers, { response: true });
    res.writeHead(response.statusCode, { ...outgoing, ...security });
    res.flushHeaders();
    response.on("error", () => res.destroy());
    response.pipe(res);
  });
  upstream.on("error", () => {
    if (res.headersSent) res.destroy();
    else jsonError(res, 502, "dev_backend_unavailable");
  });
  req.on("aborted", () => upstream.destroy());
  res.on("close", () => upstream.destroy());
  req.pipe(upstream);
}

async function serveStatic(req, res, root, pathname) {
  if (!["GET", "HEAD"].includes(req.method)) {
    jsonError(res, 405, "method_not_allowed");
    return;
  }
  let entry = files.get(pathname);
  const hashedImage = /^\/assets\/nei\/[a-f0-9]{64}\.png$/.test(pathname);
  if (hashedImage) entry = [pathname.slice(1), "image/png"];
  if (!entry) {
    jsonError(res, 404, "not_found");
    return;
  }
  try {
    const base = await realpath(root);
    const filename = await realpath(path.join(base, entry[0]));
    const relative = path.relative(base, filename);
    if (relative === ".." || relative.startsWith(`..${path.sep}`) || path.isAbsolute(relative)) {
      jsonError(res, 404, "not_found");
      return;
    }
    const info = await stat(filename);
    if (!info.isFile()) {
      jsonError(res, 404, "not_found");
      return;
    }
    res.writeHead(200, {
      ...security,
      "Content-Type": entry[1],
      "Content-Length": info.size,
      "Cache-Control": hashedImage || pathname.startsWith("/assets/brand/")
        ? "public, max-age=31536000, immutable" : "no-store",
    });
    if (req.method === "HEAD") res.end();
    else createReadStream(filename).on("error", () => res.destroy()).pipe(res);
  } catch (error) {
    jsonError(res, error.code === "ENOENT" || error.code === "ENOTDIR" ? 404 : 500,
      error.code === "ENOENT" || error.code === "ENOTDIR" ? "not_found" : "static_unavailable");
  }
}

export function createDevServer({ root = defaultRoot, backend = null } = {}) {
  const target = backendOrigin(backend);
  return http.createServer((req, res) => {
    const origin = localOrigin(req);
    if (!origin) {
      jsonError(res, 403, "forbidden");
      return;
    }
    if (!req.url?.startsWith("/") || req.url.startsWith("//") || req.url.includes("\\")) {
      jsonError(res, 400, "invalid_request");
      return;
    }
    let url;
    try { url = new URL(req.url, origin); } catch {
      jsonError(res, 400, "invalid_request");
      return;
    }
    if (url.pathname.startsWith("/api/")) proxy(req, res, target, origin);
    else void serveStatic(req, res, root, url.pathname);
  });
}

function listen(server, port) {
  return new Promise((resolve, reject) => {
    const error = (reason) => { server.off("listening", ready); reject(reason); };
    const ready = () => { server.off("error", error); resolve(server); };
    server.once("error", error);
    server.once("listening", ready);
    server.listen(port, "127.0.0.1");
  });
}

export async function startDevServer({ port = 4317, ...options } = {}) {
  if (!Number.isInteger(port) || port < 0 || port > 65535) throw new Error("Port must be 0 through 65535.");
  const server = createDevServer(options);
  try { return await listen(server, port); } catch (error) {
    if (error.code !== "EADDRINUSE" || port === 0) throw error;
    return listen(server, 0);
  }
}

async function main() {
  const options = {};
  for (const argument of process.argv.slice(2)) {
    if (argument.startsWith("--backend=")) options.backend = argument.slice("--backend=".length);
    else if (argument.startsWith("--port=")) {
      const value = argument.slice("--port=".length);
      if (!/^\d+$/.test(value)) throw new Error("Port must be 0 through 65535.");
      options.port = Number(value);
    } else throw new Error("Usage: npm run dev -- [--backend=http://127.0.0.1:8080] [--port=4317]");
  }
  const server = await startDevServer(options);
  console.log(`AE Orbit: http://127.0.0.1:${server.address().port}`);
  console.log(options.backend ? `Backend: ${new URL(options.backend).origin}` : "Backend: not configured");
  const stop = () => {
    server.close(() => {});
    server.closeAllConnections();
  };
  process.once("SIGINT", stop);
  process.once("SIGTERM", stop);
}

if (process.argv[1] && pathToFileURL(path.resolve(process.argv[1])).href === import.meta.url) {
  main().catch((error) => { console.error(error.message); process.exitCode = 1; });
}
