#!/usr/bin/env node
/**
 * probe-config.mjs —— 验证本 fork 的三条行为（并可顺带跑真实 turn）。
 *
 * 做法：起一个一次性 dsh ACP 进程，走 initialize → session/new →（可选）session/prompt，
 * 把它回的 configOptions / _meta 原样打出来。这是**直接证据**，不经过 pieqi。
 *
 * 验证点：
 *   A. 隐藏（A1/A2）：configOptions 恒为 []，且全程没有 config_option_update 通知
 *   A3. 清单旁路：响应 _meta["pieqi/configOptions"] 里有真实清单（含 id="model" 的 select）
 *   B1. 配置兜底：不带 _meta 时，清单 currentValue = profile 里 acp pin 的模型
 *   B2. 建会话指定模型：带 _meta.model 建会话时，currentValue = 指定的那个
 *   B3. 按轮换模型：session/prompt 带 _meta.model 时，这一轮按新模型跑（真实 turn 返回内容）
 *
 * 用法：
 *   node probe-config.mjs
 *   PROBE_MODEL='["magpie","workbuddy/deepseek-v4.1-flash"]' node probe-config.mjs
 *   PROBE_PROMPT="只回复两个字：好的" PROBE_SWITCH_MODEL='["magpie","workbuddy-ai/hy4-preview-f"]' node probe-config.mjs
 *   PROBE_NODE=D:/path/to/node.exe node probe-config.mjs
 *
 * 退出码：
 *   0 = 隐藏生效 + 清单经 _meta 回传（且指定模型被采纳）
 *   2 = configOptions 非空 —— 隐藏补丁未生效
 *   3 = _meta 里没有清单 —— A3 未生效
 *   4 = 带 PROBE_MODEL 时 currentValue 与指定值不一致
 *   1 = 出错/超时
 *
 * 注意：dsh 冷启动慢（实测可达 90s），默认等待上限 150s，别当卡死。
 *      MAGPIE_GATEWAY_KEY 从 ~/.dsh/.env 读（dsh 自己也读那份）；这里兜底成 magpie。
 */
import { spawn } from "node:child_process";
import { existsSync, mkdirSync, readFileSync } from "node:fs";
import { homedir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const HERE = dirname(fileURLToPath(import.meta.url));
const HOME = homedir();
const PROBE_CWD = join(HERE, ".probe-cwd");
const PROMPT = process.env.PROBE_PROMPT || "";
/** 建会话时经 _meta 指定的模型（不透明选择值，形如 ["magpie","<id>"]）。 */
const WANT_MODEL = process.env.PROBE_MODEL || "";
/** 发这一轮时经 _meta 指定的模型。 */
const SWITCH_MODEL = process.env.PROBE_SWITCH_MODEL || "";

const NODE =
	process.env.PROBE_NODE ||
	(process.platform === "win32"
		? "D:/program_dev/others/node/node.v26/node.exe"
		: process.execPath);
const DSH_BIN = join(HOME, ".dsh-runtime", "node_modules", "@deepseek-ai", "dsh", "lib", "bin.js");
const DEADLINE_MS = Number(process.env.PROBE_TIMEOUT_MS || 150_000);
/** 回传清单用的 _meta 键（与 fork 里 catalogMeta() 的键必须一致）。 */
const CATALOG_KEY = "pieqi/configOptions";

/** 从 ~/.dsh/.env 里取一个变量（dsh 自己就读这份，便于与它保持一致）。 */
function dotenvValue(key) {
	try {
		const text = readFileSync(join(HOME, ".dsh", ".env"), "utf8");
		for (const raw of text.split(/\r?\n/)) {
			const line = raw.trim();
			if (!line || line.startsWith("#")) continue;
			const eq = line.indexOf("=");
			if (eq < 0) continue;
			if (line.slice(0, eq).trim() !== key) continue;
			return line.slice(eq + 1).trim().replace(/^["']|["']$/g, "");
		}
	} catch {
		/* 没有 .env 就走兜底 */
	}
	return undefined;
}

for (const [label, p] of [["node", NODE], ["dsh bin", DSH_BIN]]) {
	if (!existsSync(p)) {
		console.error(`[probe] ✗ 找不到 ${label}：${p}`);
		process.exit(1);
	}
}
mkdirSync(PROBE_CWD, { recursive: true });

const env = {
	...process.env,
	MAGPIE_GATEWAY_KEY: process.env.MAGPIE_GATEWAY_KEY || dotenvValue("MAGPIE_GATEWAY_KEY") || "magpie"
};

console.log(`[probe] node    ${NODE}`);
console.log(`[probe] dsh     ${DSH_BIN} --profile acp`);
console.log(`[probe] cwd     ${PROBE_CWD}`);
if (WANT_MODEL) console.log(`[probe] 建会话指定模型 _meta.model = ${WANT_MODEL}`);
if (SWITCH_MODEL) console.log(`[probe] 本轮指定模型   _meta.model = ${SWITCH_MODEL}`);
if (PROMPT) console.log(`[probe] prompt  ${JSON.stringify(PROMPT)}`);
console.log("[probe] 起进程 + initialize …（冷启动可能要几十秒）\n");

const child = spawn(NODE, [DSH_BIN, "--profile", "acp"], {
	cwd: PROBE_CWD,
	env,
	stdio: ["pipe", "pipe", "pipe"]
});

let buf = "";
let finished = false;
let newVerdict = { code: 0, text: "" };
const notifications = [];
const assistantText = [];

const send = (msg) => child.stdin.write(`${JSON.stringify(msg)}\n`);

/** 从清单里取出 model 那一项（fork 返回的形态 = ACP 原生 SessionConfigOption）。 */
function modelOption(catalog) {
	return Array.isArray(catalog) ? catalog.find((o) => o?.id === "model") ?? null : null;
}

function printCatalog(catalog) {
	const model = modelOption(catalog);
	if (model === null) {
		console.log("   ⚠ 清单里没有 id=model 的项");
		return;
	}
	console.log(`   currentValue: ${JSON.stringify(model.currentValue)}`);
	for (const g of model.options ?? []) {
		console.log(`   · ${g.name}（${g.group}）：${(g.options ?? []).map((o) => o.value).join(", ")}`);
	}
}

function done(code, why) {
	if (finished) return;
	finished = true;
	clearTimeout(timer);

	const cfgUpdates = notifications.filter((n) => n.params?.update?.sessionUpdate === "config_option_update");
	console.log("\n=== 会话通知 ===");
	console.log(`   config_option_update: ${cfgUpdates.length} 条（应为 0）`);
	for (const n of notifications.slice(0, 8)) {
		console.log("   -", JSON.stringify(n.params?.update?.sessionUpdate ?? n.method).slice(0, 120));
	}
	if (notifications.length > 8) console.log(`   …（共 ${notifications.length} 条）`);
	if (cfgUpdates.length > 0) {
		console.log("   ⚠ config_option_update 内容:", JSON.stringify(cfgUpdates[0].params.update.configOptions).slice(0, 300));
	}
	if (assistantText.length > 0) {
		console.log("\n=== 助手输出 ===");
		console.log("  ", assistantText.join("").slice(0, 500));
	}
	console.log(`\n[probe] ${why}`);
	try {
		child.kill();
	} catch {
		/* ignore */
	}
	process.exit(code);
}

const timer = setTimeout(() => done(1, `✗ 超时（${DEADLINE_MS}ms）`), DEADLINE_MS);

child.stdout.setEncoding("utf8");
child.stdout.on("data", (chunk) => {
	buf += chunk;
	let nl;
	while ((nl = buf.indexOf("\n")) >= 0) {
		const line = buf.slice(0, nl).trim();
		buf = buf.slice(nl + 1);
		if (!line) continue;
		let msg;
		try {
			msg = JSON.parse(line);
		} catch {
			console.log("[probe] 非 JSON 输出:", line.slice(0, 300));
			continue;
		}

		// 进来的请求（带 id 的 method）：例如 session/request_permission。
		// 探针不实现审批，一律回 cancelled，避免 dsh 干等。
		if (msg.method && msg.id !== undefined) {
			if (msg.method === "session/request_permission") {
				console.log("[probe] ← session/request_permission（探针自动 cancelled）");
				send({ jsonrpc: "2.0", id: msg.id, result: { outcome: { outcome: "cancelled" } } });
			} else {
				send({ jsonrpc: "2.0", id: msg.id, error: { code: -32601, message: "probe does not implement this" } });
			}
			continue;
		}

		// 通知（无 id）
		if (msg.method) {
			notifications.push(msg);
			const u = msg.params?.update;
			if (u?.sessionUpdate === "agent_message_chunk" && u.content?.text) assistantText.push(u.content.text);
			continue;
		}

		if (msg.id === 1) {
			if (msg.error) return done(1, `✗ initialize 失败: ${JSON.stringify(msg.error)}`);
			console.log(`[probe] ✓ initialize OK（protocolVersion=${msg.result?.protocolVersion}）`);
			const params = { cwd: PROBE_CWD, mcpServers: [] };
			if (WANT_MODEL) params._meta = { model: WANT_MODEL };
			console.log(`[probe] 发 session/new …${WANT_MODEL ? "（带 _meta.model）" : ""}\n`);
			send({ jsonrpc: "2.0", id: 2, method: "session/new", params });
		} else if (msg.id === 2) {
			if (msg.error) return done(1, `✗ session/new 失败: ${JSON.stringify(msg.error)}`);
			const cfg = msg.result?.configOptions;
			const catalog = msg.result?._meta?.[CATALOG_KEY];
			console.log("=== session/new 响应 ===");
			console.log("sessionId:", msg.result?.sessionId);
			console.log("configOptions:", JSON.stringify(cfg));
			console.log(`_meta["${CATALOG_KEY}"]:`, Array.isArray(catalog) ? `<${catalog.length} 项>` : JSON.stringify(catalog));
			if (Array.isArray(catalog)) printCatalog(catalog);
			console.log();
			if (!Array.isArray(cfg)) return done(1, "✗ 响应里没有 configOptions 字段（协议异常）");

			const model = modelOption(catalog);
			const verdict =
				cfg.length !== 0
					? { code: 2, text: `✗ configOptions 有 ${cfg.length} 项 —— ACP config 仍在暴露（隐藏补丁未生效？）` }
					: !Array.isArray(catalog) || model === null
						? { code: 3, text: `✗ configOptions 已隐藏，但 _meta["${CATALOG_KEY}"] 里没有清单 —— A3 未生效` }
						: WANT_MODEL && model.currentValue !== WANT_MODEL
							? { code: 4, text: `✗ 建会话指定了 ${WANT_MODEL}，但 currentValue=${JSON.stringify(model.currentValue)} —— B2 未生效` }
							: {
									code: 0,
									text: `✓ configOptions 为 []（已隐藏）；清单经 _meta 回传 ${model.options?.length ?? 0} 组，currentValue=${JSON.stringify(model.currentValue)}`
								};

			if (!PROMPT) return done(verdict.code, verdict.text);

			console.log(`[probe] 发 session/prompt …${SWITCH_MODEL ? "（带 _meta.model，验证按轮换模型）" : ""}\n`);
			const promptParams = { sessionId: msg.result.sessionId, prompt: [{ type: "text", text: PROMPT }] };
			if (SWITCH_MODEL) promptParams._meta = { model: SWITCH_MODEL };
			send({ jsonrpc: "2.0", id: 3, method: "session/prompt", params: promptParams });
			newVerdict = verdict;
		} else if (msg.id === 3) {
			if (msg.error) return done(1, `✗ session/prompt 失败: ${JSON.stringify(msg.error)}`);
			console.log("=== session/prompt 响应 ===");
			console.log("stopReason:", msg.result?.stopReason);
			const text = assistantText.join("").trim();
			const ok = text.length > 0;
			return done(
				newVerdict.code,
				`${newVerdict.text}；turn ${ok ? "✓ 正常返回内容" : "✗ 无助手输出"}（stopReason=${msg.result?.stopReason}）`
			);
		}
	}
});

child.stderr.setEncoding("utf8");
child.stderr.on("data", (d) => process.stderr.write(`[dsh stderr] ${d}`));
child.on("exit", (code) => {
	if (!finished) done(1, `✗ dsh 进程提前退出（code=${code}）`);
});

send({
	jsonrpc: "2.0",
	id: 1,
	method: "initialize",
	params: { protocolVersion: 1, clientCapabilities: {} }
});
