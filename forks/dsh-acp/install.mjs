#!/usr/bin/env node
/**
 * install.mjs —— 把本目录打过补丁的 lib/index.js 装进 dsh 运行时的 @deepseek-ai/dsh-acp。
 *
 * 为什么需要它：dsh 是本机独立安装的 npm 包（~/.dsh-runtime），它不认「本地 fork」这种概念。
 * 让 dsh 用到我们的改动，唯一不需要它配合的办法就是把补丁后的文件覆盖回它的 node_modules。
 * 这个脚本把「覆盖 + 备份 + 校验」做成可重复的一步操作。
 *
 * 用法：
 *   node install.mjs              安装（先备份 runtime 现状，再校验语法与 sha256）
 *   node install.mjs --check      只比对当前 runtime 是否已是本 fork 的版本（不写）
 *   node install.mjs --restore    还原成上游原版（用 lib/index.orig.js）
 *
 * 环境变量：
 *   DSH_ACP_TARGET  覆盖目标包目录（默认 <home>/.dsh-runtime/node_modules/@deepseek-ai/dsh-acp）
 *
 * 何时需要重跑：dsh 升级、或在 ~/.dsh-runtime 里跑过 npm install/ci 之后（会把文件换回官方版）。
 */
import { createHash } from "node:crypto";
import { spawnSync } from "node:child_process";
import { copyFileSync, existsSync, mkdirSync, readdirSync, readFileSync, writeFileSync } from "node:fs";
import { homedir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const HERE = dirname(fileURLToPath(import.meta.url));
const FORK_LIB = join(HERE, "lib", "index.js");        // 补丁版（本目录的真相）
const UPSTREAM_LIB = join(HERE, "lib", "index.orig.js"); // 上游原版留档
const BACKUP_DIR = join(HERE, "backups");

const TARGET_DIR =
	process.env.DSH_ACP_TARGET ||
	join(homedir(), ".dsh-runtime", "node_modules", "@deepseek-ai", "dsh-acp");
const TARGET_LIB = join(TARGET_DIR, "lib", "index.js");

const argv = process.argv.slice(2);
const mode = argv.includes("--restore") ? "restore" : argv.includes("--check") ? "check" : "install";

const sha256 = (p) => createHash("sha256").update(readFileSync(p)).digest("hex");
const stamp = () => new Date().toISOString().replace(/[-:]|\.\d+/g, "").replace("T", "-");

function fail(msg) {
	console.error(`[dsh-acp-fork] ✗ ${msg}`);
	process.exit(1);
}

if (!existsSync(TARGET_DIR)) fail(`找不到目标包目录：${TARGET_DIR}\n（可用 DSH_ACP_TARGET 指定，或确认 dsh 是否已安装）`);
if (!existsSync(FORK_LIB)) fail(`找不到本 fork 的 ${FORK_LIB}`);

const source = mode === "restore" ? UPSTREAM_LIB : FORK_LIB;
if (!existsSync(source)) fail(`找不到源文件：${source}`);
const sourceSha = sha256(source);

// --- --check：只比对 ---
if (mode === "check") {
	if (!existsSync(TARGET_LIB)) fail(`目标文件不存在：${TARGET_LIB}`);
	const same = sha256(TARGET_LIB) === sourceSha;
	console.log(`[dsh-acp-fork] runtime sha256 = ${sha256(TARGET_LIB)}`);
	console.log(`[dsh-acp-fork] fork    sha256 = ${sourceSha}`);
	console.log(same ? "[dsh-acp-fork] ✓ runtime 已是本 fork 的版本" : "[dsh-acp-fork] ✗ runtime 与 fork 不一致（需要重跑 install）");
	process.exit(same ? 0 : 2);
}

// --- 安装 / 还原 ---
if (!existsSync(TARGET_LIB)) fail(`目标文件不存在：${TARGET_LIB}`);
const beforeSha = sha256(TARGET_LIB);

if (beforeSha === sourceSha) {
	console.log(`[dsh-acp-fork] ✓ 已是最新（sha256=${sourceSha.slice(0, 12)}），无需改动`);
	process.exit(0);
}

// 备份现状：文件名以内容 sha 开头 ⇒ 同一份内容反复安装只留一个备份（不会堆）。
mkdirSync(BACKUP_DIR, { recursive: true });
const sha12 = beforeSha.slice(0, 12);
const existingBackup = readdirSync(BACKUP_DIR).find((f) => f.startsWith(`index.prev-${sha12}-`));
let backupPath = existingBackup ? join(BACKUP_DIR, existingBackup) : "";
if (backupPath === "") {
	backupPath = join(BACKUP_DIR, `index.prev-${sha12}-${stamp()}.js`);
	copyFileSync(TARGET_LIB, backupPath);
}

// 先写到临时文件再改名覆盖：避免「写一半」被别的进程读到半个文件。
const tmpPath = `${TARGET_LIB}.forktmp`;
writeFileSync(tmpPath, readFileSync(source));
copyFileSync(tmpPath, TARGET_LIB);
try {
	spawnSync(process.execPath, ["-e", "require('node:fs').unlinkSync(process.argv[1])", tmpPath], { stdio: "ignore" });
} catch {
	/* 临时文件清理失败不影响结果 */
}

// 校验：语法 + 落盘内容
const check = spawnSync(process.execPath, ["--check", TARGET_LIB], { encoding: "utf8" });
if (check.status !== 0) fail(`安装后语法校验失败：\n${check.stderr || check.stdout}`);
const afterSha = sha256(TARGET_LIB);
if (afterSha !== sourceSha) fail(`落盘内容 sha256 不符：期望 ${sourceSha}，实际 ${afterSha}`);

console.log(`[dsh-acp-fork] ✓ ${mode === "restore" ? "已还原为上游原版" : "已安装补丁版"}`);
console.log(`[dsh-acp-fork]   目标   ${TARGET_LIB}`);
console.log(`[dsh-acp-fork]   备份   ${backupPath}（安装前的内容）`);
console.log(`[dsh-acp-fork]   sha256 ${afterSha.slice(0, 12)}（语法校验通过）`);
console.log("[dsh-acp-fork] 注意：新起的 dsh 会话才会加载改动；已在跑的 dsh 进程不受影响。");
