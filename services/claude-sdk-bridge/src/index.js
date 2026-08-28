// src/index.js
// Pieqi → Claude Agent SDK 常驻桥：HTTP + SSE。
// 私有服务（默认 127.0.0.1:18790），仅 pieqi 的 claude agent 包使用，不对外暴露。
//
// API（对应 multi-agent.md §5.3）：
//   POST   /v1/sessions                   创建会话（可选 resumeSdkSessionId）
//   POST   /v1/sessions/:id/prompt        追加一轮（非阻塞 202；事件走 SSE）
//   GET    /v1/sessions/:id/events        SSE 事件流（连接时回放历史 ring）
//   POST   /v1/sessions/:id/permissions/:rid  审批 allow/deny
//   POST   /v1/sessions/:id/cancel        取消当前轮
//   DELETE /v1/sessions/:id               结束会话（杀子进程）
//   GET    /v1/health                     探活
//
// 事件（SSE event 名 = 中性事件 kind）：
//   text_delta / thinking_delta / tool_start / tool_end /
//   permission_needed / turn_end / error / state_changed
import { createServer } from "node:http";
import { randomUUID } from "node:crypto";
import { SessionRuntime } from "./session.js";

const PORT = Number(process.env.BRIDGE_PORT || 18790);
const HOST = process.env.BRIDGE_HOST || "127.0.0.1";
const HISTORY_MAX = Number(process.env.BRIDGE_HISTORY_MAX || 500); // SSE 重连回放窗口
const IDLE_TIMEOUT_MS = Number(process.env.BRIDGE_IDLE_TIMEOUT_MS || 30 * 60 * 1000);
const BRIDGE_TOKEN = process.env.BRIDGE_TOKEN || ""; // 非空时 /v1 业务路由校验 Bearer token（health 保持开放供探活）

const sessions = new Map(); // id -> SessionRuntime
const history = new Map(); // id -> Event[]（ring）
const sseClients = new Map(); // id -> Set<res>

// ---- 事件历史（SSE 重连不丢事件的轻量回放）----

function pushHistory(id, ev) {
  const h = history.get(id);
  if (!h) return;
  h.push(ev);
  if (h.length > HISTORY_MAX) h.splice(0, h.length - HISTORY_MAX);
}

function eventSinkFor(id) {
  let seq = 0;
  return (ev) => {
    seq += 1;
    pushHistory(id, { seq, ...ev });
    const clients = sseClients.get(id);
    if (clients) {
      for (const res of clients) {
        if (res.writableEnded) continue;
        writeEvent(res, { seq, ...ev });
      }
    }
  };
}

function writeEvent(res, ev) {
  // ev.kind 为事件名；SSE 注释行 + data 为 JSON（含 kind 便于客户端兜底）
  res.write(`event: ${ev.kind}\ndata: ${JSON.stringify(ev)}\n\n`);
}

// ---- 会话生命周期 ----

function createSession(body) {
  const id = randomUUID();
  const sink = eventSinkFor(id);
  history.set(id, []);
  sseClients.set(id, new Set());
  const runtime = new SessionRuntime({
    id,
    cwd: body.cwd,
    resumeSdkSessionId: body.resumeSdkSessionId ?? null,
    eventSink: sink,
    onClosed: () => cleanupSession(id),
  });
  sessions.set(id, runtime);
  return { id };
}

function cleanupSession(id) {
  sessions.delete(id);
  history.delete(id);
  const clients = sseClients.get(id);
  if (clients) {
    for (const res of clients) {
      if (!res.writableEnded) {
        res.write(`event: state_changed\ndata: ${JSON.stringify({ kind: "state_changed", state: "closed" })}\n\n`);
        res.end();
      }
    }
    sseClients.delete(id);
  }
}

// ---- HTTP 工具 ----

function sendJson(res, status, obj) {
  const body = JSON.stringify(obj);
  res.writeHead(status, { "Content-Type": "application/json" });
  res.end(body);
}

function sendError(res, status, message) {
  sendJson(res, status, { error: message });
}

function parseBody(req) {
  return new Promise((resolve, reject) => {
    let data = "";
    req.on("data", (c) => {
      data += c;
      if (data.length > 1_000_000) {
        reject(new Error("body too large"));
        req.destroy();
      }
    });
    req.on("end", () => {
      if (!data) return resolve({});
      try {
        resolve(JSON.parse(data));
      } catch {
        reject(new Error("invalid json"));
      }
    });
    req.on("error", reject);
  });
}

function attachSSE(req, res, id) {
  res.writeHead(200, {
    "Content-Type": "text/event-stream",
    "Cache-Control": "no-cache",
    Connection: "keep-alive",
    "X-Accel-Buffering": "no",
  });
  res.write(": connected\n\n");
  // 回放历史 ring（重连不丢事件）
  for (const ev of history.get(id) ?? []) writeEvent(res, ev);
  const clients = sseClients.get(id);
  if (clients) {
    clients.add(res);
    req.on("close", () => clients.delete(res));
  }
}

// 鉴权：BRIDGE_TOKEN 非空时要求 Authorization: Bearer <token>。
// 用长度安全比较避免简单字符串比较的时序差异（本地端口，防御性）。
function authOk(req) {
  if (!BRIDGE_TOKEN) return true;
  const header = req.headers.authorization ?? "";
  const prefix = "Bearer ";
  if (!header.startsWith(prefix)) return false;
  const got = header.slice(prefix.length);
  if (got.length !== BRIDGE_TOKEN.length) return false;
  let diff = 0;
  for (let i = 0; i < got.length; i++) diff |= got.charCodeAt(i) ^ BRIDGE_TOKEN.charCodeAt(i);
  return diff === 0;
}

// 路由：/v1 下除 /v1/health 外全部校验 token（health 供 Go 侧探活，保持开放）。
function tokenRequired(method, parts) {
  if (!BRIDGE_TOKEN) return false;
  if (parts[0] !== "v1") return false;
  if (method === "GET" && parts[1] === "health") return false;
  return true;
}

// ---- 路由 ----

async function route(req, res) {
  const url = new URL(req.url, `http://${HOST}`);
  const parts = url.pathname.split("/").filter(Boolean); // ['v1', 'sessions', ...]
  const method = req.method;

  if (tokenRequired(method, parts) && !authOk(req)) {
    return sendError(res, 401, "unauthorized");
  }

  // GET /v1/health
  if (method === "GET" && parts[0] === "v1" && parts[1] === "health") {
    return sendJson(res, 200, { ok: true, sessions: sessions.size });
  }

  // POST /v1/sessions
  if (method === "POST" && parts[0] === "v1" && parts[1] === "sessions" && parts.length === 2) {
    const body = await parseBody(req);
    if (!body.cwd) return sendError(res, 400, "cwd required");
    const { id } = createSession(body);
    return sendJson(res, 201, { id });
  }

  // /v1/sessions/:id/...
  if (parts[0] === "v1" && parts[1] === "sessions" && parts.length >= 3) {
    const sid = parts[2];
    const runtime = sessions.get(sid);

    // POST /v1/sessions/:id/prompt
    if (method === "POST" && parts[3] === "prompt") {
      if (!runtime) return sendError(res, 404, "session not found");
      const body = await parseBody(req);
      if (typeof body.text !== "string" || body.text.trim() === "") {
        return sendError(res, 400, "text required");
      }
      if (!runtime.pushPrompt(body.text, body.clientRef)) return sendError(res, 409, "session closed");
      return sendJson(res, 202, { ok: true });
    }

    // GET /v1/sessions/:id/events
    if (method === "GET" && parts[3] === "events") {
      if (!runtime) return sendError(res, 404, "session not found");
      return attachSSE(req, res, sid);
    }

    // POST /v1/sessions/:id/permissions/:rid
    if (method === "POST" && parts[3] === "permissions" && parts[4]) {
      if (!runtime) return sendError(res, 404, "session not found");
      const rid = parts[4];
      const body = await parseBody(req);
      const allow = body.allow === true;
      if (!runtime.respondPermission(rid, allow, body.optionID)) {
        return sendError(res, 404, `no pending permission for rid ${rid}`);
      }
      return sendJson(res, 200, { resolved: true });
    }

    // POST /v1/sessions/:id/cancel
    if (method === "POST" && parts[3] === "cancel") {
      if (!runtime) return sendError(res, 404, "session not found");
      await runtime.cancel();
      return sendJson(res, 202, { ok: true });
    }

    // DELETE /v1/sessions/:id
    if (method === "DELETE" && parts.length === 3) {
      if (!runtime) return sendError(res, 404, "session not found");
      runtime.close();
      return sendJson(res, 200, { closed: true });
    }
  }

  return sendError(res, 404, `no route: ${method} ${url.pathname}`);
}

// ---- 启动 ----

const server = createServer((req, res) => {
  route(req, res).catch((err) => {
    sendError(res, 400, err?.message ?? "bad request");
  });
});

// idle 回收：超过 IDLE_TIMEOUT_MS 无事件活动的会话关闭（防孤儿进程）
setInterval(() => {
  const now = Date.now();
  for (const [id, runtime] of sessions) {
    if (!runtime.lastActivity) runtime.lastActivity = now;
    if (now - runtime.lastActivity > IDLE_TIMEOUT_MS && runtime.state !== "running") {
      runtime.close();
    }
  }
}, 60_000).unref();

// 优雅关停：关全部会话再退
function shutdown() {
  for (const runtime of sessions.values()) runtime.close();
  server.close(() => process.exit(0));
  setTimeout(() => process.exit(0), 2000).unref();
}
process.on("SIGINT", shutdown);
process.on("SIGTERM", shutdown);

server.listen(PORT, HOST, () => {
  const addr = server.address();
  const actualPort = addr?.port ?? PORT;
  console.log(`[bridge] claude-sdk-bridge listening on http://${HOST}:${actualPort} (sessions=${sessions.size})`);
});
