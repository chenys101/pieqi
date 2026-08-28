// src/session.js
// 一个 bridge 会话的运行时：把官方 Agent SDK 的常驻多轮 query 包装成
// 中性事件流 + 审批挂起/释放，供 HTTP 层（index.js）驱动。
//
// 关键设计（来自 spike 结论，见 docs/multi-agent-evaluation.md §8.4）：
//   - includePartialMessages: true —— 必需，否则没有 text_delta 增量，只有整块 assistant 消息
//   - settings.permissions.ask 强制走 ask 路径 —— 否则本机默认会直接放行 shell 工具
//   - canUseTool 返回挂起 Promise = 审批挂起；rid 由 host 经 respondPermission 释放
//   - signal abort 时释放挂起（cancel 不泄漏到下一轮）
//   - result 消息 = 一轮结束（turn_end），带 sdkSessionId / usage / cost
//   - 子进程崩溃 = query 抛错 → error 事件（不挂死）
import { query } from "@anthropic-ai/claude-agent-sdk";
import { randomUUID } from "node:crypto";

// 强制所有工具经 canUseTool 审批（host 侧决定放行/上卡）。
// 注：若该通配写法不被 SDK 接受（ask 规则校验失败），改回显式清单，如
// ['Bash', 'PowerShell', 'Edit', 'Write', 'WebFetch', 'Task', 'Agent']。
const ASK_ALL = ["*"];

// 只读/派生类工具自动放行：命中即在 _canUseTool 里直接 allow，不产生 permission_needed、
// 不占审批卡。（P5 修复）agent 一次并行多个只读工具（Grep/Glob/Read…）若都卡审批，
// 会叠加后端单决策槽互相覆盖 → 部分审批在 UI 不可见、任务假死到超时。只有变更类工具需要
// 人类审批，对齐 config.yaml 的 pieqi.hook_tools 意图（Bash/Write/Edit/NotebookEdit）。
const AUTO_ALLOW_TOOLS = new Set([
  "Grep", "Glob", "Read", "WebSearch", "WebFetch",
  "TodoWrite", "Agent", "Task", "Skill", "KillShell",
]);

export class SessionRuntime {
  /**
   * @param {object} opts
   * @param {string} opts.id            桥会话 id（uuid）
   * @param {string} opts.cwd           工作目录
   * @param {string|null} opts.resumeSdkSessionId 续问的 SDK 会话 id（可空）
   * @param {(ev: object) => void} opts.eventSink 事件出口（HTTP 层做广播 + 历史）
   * @param {object} [opts.logger]
   * @param {() => void} [opts.onClosed] 会话关闭回调（HTTP 层清理）
   */
  constructor(opts) {
    this.id = opts.id;
    this.cwd = opts.cwd;
    this.eventSink = opts.eventSink;
    this.logger = opts.logger ?? console;
    this.onClosed = opts.onClosed;

    this.state = "idle"; // idle | running | waiting_permission | closed
    this.sdkSessionId = opts.resumeSdkSessionId ?? null;
    this.turnSeq = 1; // 当前轮的序号；result 后 +1
    this.closed = false;

    this.queue = []; // 已入队的用户消息 {text, ref}（等待 SDK 拉取下一轮）
    this.waiters = []; // SDK 正等在 generator.next() 上的解析器
    this.pendingPerms = new Map(); // rid -> resolve(allow|deny)
    this.pendingTools = new Map(); // toolUseId -> {title, input}
    this.query = null;
    this.currentClientRef = null; // 当前正在处理的消息的 clientRef（turn_end 回带，host 精确关联）
    this.turnInFlight = false; // 有未结束的轮（cancel 据此补合成 turn_end）

    this._start();
  }

  // ---- 事件出口 ----

  _emit(kind, data = {}) {
    this.lastActivity = Date.now();
    this.eventSink({ kind, sessionId: this.id, turnSeq: this.turnSeq, state: this.state, ...data });
  }

  _setState(s) {
    if (this.state === s) return;
    this.state = s;
    this._emit("state_changed", { state: s });
  }

  // ---- SDK 启动与消费 ----

  _start() {
    const options = {
      cwd: this.cwd,
      permissionMode: "default",
      tools: { type: "preset", preset: "claude_code" },
      // 清掉继承的 allow 规则并强制所有工具走 ask → canUseTool（host 全权审批）
      settings: { permissions: { allow: [], deny: [], ask: ASK_ALL } },
      includePartialMessages: true,
      maxTurns: 200,
      canUseTool: (toolName, input, ctl) => this._canUseTool(toolName, input, ctl),
    };
    if (this.sdkSessionId) options.resume = this.sdkSessionId;
    this.query = query({ prompt: this.promptStream(), options });
    this._consume();
    this.logger.debug(`[bridge] session ${this.id} started (resume=${this.sdkSessionId ?? "none"}) cwd=${this.cwd}`);
  }

  async _consume() {
    try {
      for await (const msg of this.query) {
        this._onMessage(msg);
      }
      // 正常收尾（input 流结束）
      if (!this.closed) {
        this._emit("state_changed", { state: "closed" });
      }
    } catch (err) {
      // 子进程崩溃 / 连接断开：抛错而非挂死（spike U3 结论）
      if (!this.closed) {
        this.logger.error(`[bridge] session ${this.id} stream error: ${err?.message ?? err}`);
        this._emit("error", { message: String(err?.message ?? err) });
      }
    } finally {
      this._cleanup();
    }
  }

  _onMessage(msg) {
    switch (msg.type) {
      case "system": {
        // init 带 session 元数据（含 sdk session_id / model 等）；其他子类型忽略
        if (msg.session_id) this.sdkSessionId = msg.session_id;
        break;
      }
      case "stream_event": {
        const e = msg.event;
        if (e.type === "content_block_delta") {
          const d = e.delta;
          if (d?.type === "text_delta" && d.text) {
            this._emit("text_delta", { text: d.text, isThought: false });
          } else if (d?.type === "thinking_delta" && d.thinking) {
            this._emit("thinking_delta", { text: d.thinking, isThought: true });
          }
        }
        break;
      }
      case "assistant": {
        for (const block of msg.message?.content ?? []) {
          if (block.type === "tool_use") {
            this.pendingTools.set(block.id, { title: block.name, input: block.input ?? null });
            this._emit("tool_start", {
              toolCallId: block.id,
              toolTitle: block.name,
              toolKind: "",
              rawInput: block.input ?? null,
            });
          }
        }
        break;
      }
      case "user": {
        // tool_result 回显 → 对应该工具调用收尾
        const content = Array.isArray(msg.message?.content) ? msg.message.content : [];
        for (const block of content) {
          if (block.type === "tool_result" && block.tool_use_id) {
            const info = this.pendingTools.get(block.tool_use_id) ?? {};
            this.pendingTools.delete(block.tool_use_id);
            this._emit("tool_end", {
              toolCallId: block.tool_use_id,
              toolTitle: info.title ?? "",
              toolStatus: block.is_error === true ? "failed" : "completed",
              rawOutput: block.content ?? null,
            });
          }
        }
        break;
      }
      case "result": {
        // 一轮结束 = turn 边界（spike U1：每轮恰好一条 result）
        const cost = typeof msg.total_cost_usd === "number" ? msg.total_cost_usd : 0;
        this.sdkSessionId = msg.session_id ?? this.sdkSessionId;
        this.turnInFlight = false;
        this._emit("turn_end", {
          subtype: msg.subtype,
          isError: msg.is_error === true,
          clientRef: this.currentClientRef,
          turn: { resumeId: this.sdkSessionId, usage: msg.usage ?? null, costUsd: cost },
        });
        this.turnSeq += 1;
        this._setState("idle");
        break;
      }
      default:
        break;
    }
  }

  // ---- 多轮输入（streaming input 保活）----

  async *promptStream() {
    while (!this.closed) {
      const item = await this._nextPrompt();
      if (this.closed || item == null) return;
      this.currentClientRef = item.ref ?? null;
      this.turnInFlight = true;
      yield { type: "user", message: { role: "user", content: item.text }, parent_tool_use_id: null };
    }
  }

  _nextPrompt() {
    if (this.queue.length > 0) return Promise.resolve(this.queue.shift());
    return new Promise((resolve) => this.waiters.push(resolve));
  }

  /** 入队一轮用户消息（HTTP /prompt 调用）。clientRef 供 turn_end 回带，host 精确匹配本轮。关闭后返回 false。 */
  pushPrompt(text, clientRef) {
    if (this.closed) return false;
    const item = { text, ref: clientRef ?? null };
    if (this.waiters.length > 0) {
      this.waiters.shift()(item);
    } else {
      this.queue.push(item);
    }
    this._setState("running");
    return true;
  }

  // ---- 审批挂起/释放 ----

  _canUseTool(toolName, input, { toolUseID, requestId, signal }) {
    // 只读/派生类工具自动放行（不占审批卡；见 AUTO_ALLOW_TOOLS 注释）。
    if (AUTO_ALLOW_TOOLS.has(toolName)) {
      return Promise.resolve({ behavior: "allow", updatedInput: undefined });
    }
    const rid = requestId ?? toolUseID ?? randomUUID();
    return new Promise((resolve) => {
      this.pendingPerms.set(rid, resolve);
      if (signal) {
        // cancel / interrupt 时释放挂起（不泄漏到下一轮）
        signal.addEventListener(
          "abort",
          () => {
            if (this.pendingPerms.delete(rid)) {
              resolve({ behavior: "deny", message: "cancelled by host" });
            }
          },
          { once: true }
        );
      }
      this._setState("waiting_permission");
      this._emit("permission_needed", {
        reqId: rid,
        toolName,
        toolUseID,
        requestId,
        rawInput: input ?? null,
      });
    });
  }

  /** 审批响应（HTTP /permissions/:rid）。未找到 rid 返回 false。 */
  respondPermission(rid, allow, optionID) {
    const resolve = this.pendingPerms.get(rid);
    if (!resolve) return false;
    this.pendingPerms.delete(rid);
    if (allow) {
      resolve({ behavior: "allow", updatedInput: undefined });
    } else {
      resolve({ behavior: "deny", message: optionID ? `denied: ${optionID}` : "denied by user" });
    }
    this._setState("running");
    return true;
  }

  // ---- 取消 / 关闭 ----

  /** 取消当前轮（streaming-input 模式的 interrupt）。 */
  async cancel() {
    if (!this.query || this.closed) return;
    try {
      await this.query.interrupt();
    } catch (err) {
      this.logger.warn(`[bridge] session ${this.id} interrupt: ${err?.message ?? err}`);
    }
    // 被中断的轮若还没出 result，补一条合成 turn_end（host 的 Prompt 靠它返回，不挂死）
    if (this.turnInFlight) {
      this.turnInFlight = false;
      this._emit("turn_end", {
        subtype: "cancelled",
        isError: true,
        clientRef: this.currentClientRef,
        turn: { resumeId: this.sdkSessionId, usage: null, costUsd: 0 },
      });
      this.turnSeq += 1;
      this._setState("idle");
    }
  }

  /** 关闭会话：结束 input 流 + close() 杀子进程 + 释放挂起。幂等。 */
  close() {
    if (this.closed) return;
    this.closed = true;
    this._setState("closed");
    // 释放所有挂起审批（不让 canUseTool 死等）
    for (const resolve of this.pendingPerms.values()) {
      resolve({ behavior: "deny", message: "session closed" });
    }
    this.pendingPerms.clear();
    try {
      this.query?.close();
    } catch (err) {
      this.logger.warn(`[bridge] session ${this.id} close: ${err?.message ?? err}`);
    }
  }

  _cleanup() {
    this._setState("closed");
    this.onClosed?.();
  }
}
