# DSH 沙箱实现机制调查报告

**调查对象**：`@deepseek-ai/dsh` v0.2.0-rc.2，安装于
`D:\program_dev\others\node\node.v20\node_modules\@deepseek-ai\dsh\`
**DSH home**：`C:\Users\13631\.dsh\`
**调查性质**：只读取证。**未修改任何文件。**
**方法**：只读已发布的 `lib/*.js`（构建产物）、`lib/types/*.d.ts`（含完整 JSDoc 源码级注释）、`README*.md` 与 profile 配置。

> 标注约定：**【已核实】** = 在代码中直接读到；**【未能确定】** = 代码中未能读到，明确说明。

---

## 0. 关键结论速览（先读这段）

| 项 | 结论 |
|---|---|
| 沙箱策略服务 | `SandboxPolicyService`，服务名 `ctx.sandboxPolicy`，包 `@deepseek-ai/dsh-sandbox-policy` |
| mode 枚举 | `read-only` / `workspace-write` / `danger-full-access`，默认 **`read-only`** |
| Windows 机制 | **不是 AppContainer**；是 `CreateRestrictedToken` + `WRITE_RESTRICTED` 受限令牌 + 能力 SID ACL |
| SID 生成 | **`AllocateAndInitializeSid` 未被调用**；SID 是**字符串手工拼接**（sha256 派生 `S-1-4-x-y`），再由 `ConvertStringSidToSidW` 解析 |
| 进程启动 | `CreateProcessAsUserW`（**不是** `CreateProcessWithTokenW`，**没有** `STARTUPINFOEX`） |
| ACL 三元组 | `GRANT_MASK = 1114454 = 0x110156` 恰好 = `(W,D,DC)`；外加 world-Deny 与 Low 强制标签 |
| 文件工具围栏 | `SandboxedFileSystem`（继承 `LocalFileSystem`），错误码 **`FS_SANDBOX_DENIED`** |
| danger-full-access → approval never | **【已核实，属实】** 见 `dsh-base/cordis.patch.yml:248` |
| 只读 pwsh ConstrainedLanguage | **【已核实，属实】** 但**不是 DSH 主动设置**——是 ACL 副产物 |
| 命名管道 EPERM | **【已核实】** 因 `CreatePipe(..., null, 0)` 传 null 安全属性 |

---

## 1. 沙箱策略的定义位置与配置 schema

### 1.1 三个协作的服务

| 服务名 | 包 | 文件 |
|---|---|---|
| `ctx.sandboxPolicy` | `@deepseek-ai/dsh-sandbox-policy` | `...\dsh-sandbox-policy\lib\index.js` |
| `ctx.sandbox` | `@deepseek-ai/dsh-sandbox-local`（`LocalSandboxProvider`） | `...\dsh-sandbox-local\lib\index.js` |
| `ctx.fs` | `@deepseek-ai/dsh-fs-sandbox`（`SandboxedFileSystem`） | `...\dsh-fs-sandbox\lib\index.js` |
| （抽象基类） | `@deepseek-ai/dsh-sandbox`（`SandboxProvider`） | `...\dsh-sandbox\lib\index.js` |

### 1.2 mode 枚举与配置 schema

`dsh-sandbox-policy\lib\index.js:26-30`：

```js
const SANDBOX_MODES = [
	"read-only",
	"workspace-write",
	"danger-full-access"
];
```

`SandboxPolicyService.Config`（同文件 `:97-104`）：

```js
static Config = z$1.object({
    mode: z$1.union([
        "read-only",
        "workspace-write",
        "danger-full-access"
    ]).default("read-only"),
    workspaceRoot: z$1.string()
});
```

**注意**：schema 里 **没有 `allowedDirs`**。官方 `workspaceRoot` 是唯一根。

### 1.3 三种 mode 的确切语义

由 `renderPolicyContext()`（`:72-83`）渲染进系统提示词，逐字：

- **`read-only`**：`"Current DSH file policy: read-only. Any available operation enforced by the DSH file sandbox cannot modify files in the standing mode."`
- **`workspace-write`**：`"Current DSH file policy: workspace-write. Any available operation enforced by the DSH file sandbox may modify files under the session workspace: ${...}. Some platform temporary areas may also be writable."`
- **`danger-full-access`**：`"Current DSH file policy: danger-full-access. The DSH file sandbox does not restrict file modifications by available operations."`

> 你运行时的那句 "Some platform temporary areas may also be writable." 就出自此处。

### 1.4 优先级解析

`resolve()`（`:141-148`）：

```js
resolve(request = {}) {
    const { session } = request;
    return {
        mode: request.mode ?? (session === void 0 ? void 0 : this.overrideOf(session)) ?? this.defaultMode,
        workspaceRoot: resolveWorkspaceRoot(session?.header.cwd ?? this.workspaceRoot),
        ...session === void 0 ? {} : { sessionId: session.id }
    };
}
```

优先级：**一次性的已批准升级 `request.mode` > 会话日志 `sandbox/mode` 事件 > 部署默认值**。
`workspaceRoot` 取 **会话 cwd**，否则取配置根。`resolveWorkspaceRoot`（`:67-70`）只校验绝对路径。

会话内切换通过 `setSandboxMode(session, mode)`（`:40-42`）写一条 `sandbox/mode` 事件（log-only，可重放）。

### 1.5 本机实际生效的配置

`D:\program_dev\others\node\node.v20\node_modules\@deepseek-ai\dsh\node_modules\@deepseek-ai\dsh-base\cordis.patch.yml:226-233`：

```yaml
- id: sandbox
  name: '@deepseek-ai/dsh-sandbox-local'

- id: sandbox-policy
  name: '@deepseek-ai/dsh-sandbox-policy'
  config:
    mode: !!js process.env.DSH_PERMISSION_MODE ?? 'workspace-write'
    workspaceRoot: !!js process.cwd()
```

---

## 2. Windows 实现机制（核心）

包：`@deepseek-ai\dsh-sandbox-windows-acl`
- `lib\index.js`（2 行，纯 re-export）
- `lib\runner.js`（188 行，argv 前缀包装器）
- `lib\types-Cl_DXjhk.js`（**1411 行，全部实现**）
- `lib\types\*.d.ts`（按模块切分的完整 JSDoc）

### 2.1 自述的机制血缘

`types-Cl_DXjhk.js:1144-1146`：

> Mirrors the mechanism of github.com/huoyaoyuan/**windows-acl-restrict-poc @ 10e4dfb** (the fixed revision): a `WRITE_RESTRICTED` token whose restricting SIDs include distinct workspace and temp write SIDs that this sandbox adds to their owning directories' DACLs — **the intersection check then allows writes exactly where either capability has a Write ACE**.

**明确不是 AppContainer**（`AppContainer` 在代码中零命中）。

### 2.2 SID 是**怎么创建的**（关键澄清）

**没有调用 `AllocateAndInitializeSid`。**（该标识符全树零命中）

SID 是**手工字符串拼接**的：

`types-Cl_DXjhk.js:1044-1058`：

```js
function workspaceWriteSid(workspaceRoot) {
	const digest = createHash("sha256").update(workspaceRoot, "utf8").digest();
	return `S-1-4-${digest.readUInt32LE(0) % (2 ** 30 - 1) + 1}-${digest.readUInt32LE(4) % (2 ** 30 - 1) + 1}`;
}
function tempWriteSid(tempDir) {
	const digest = createHash("sha256").update("temp\0", "utf8").update(tempDir, "utf8").digest();
	return `S-1-4-${digest.readUInt32LE(0) % (2 ** 30 - 1) + 1}-${digest.readUInt32LE(4) % (2 ** 30 - 1) + 1}-1`;
}
```

**我已独立复现**（sha256 + `readUInt32LE` 取模），对 `D:\code\go\pieqi` 得 `S-1-4-288406013-887022677`。

- **`S-1-4`** = SECURITY_NULL_RID 权限域 + 自定义子授权。这是**非保留、非注册**的 SID 空间，所以无需 `AllocateAndInitializeSid`——Windows 允许任何合法 `S-1-4-x-y` 字符串。
- **workspace SID 是确定性的**：跨会话/重启/进程同一工作区 → 同一 SID（`S-1-4-x-y`，两个子授权）。
- **temp SID 第三子授权固定为 `-1`**，用于**域分离**，防止与 workspace SID 碰撞。

之后由 `ConvertStringSidToSidW` 解析成二进制 SID（`:952`、`:1257`）：

```js
if (bindings.convertStringSidToSidW(writeSid, sidSlot) === 0) throwLastError$1(bindings, "ConvertStringSidToSidW", writeSid);
```

### 2.3 受限令牌如何创建

`createRestrictedToken()`（`:884-897`）：

```js
function createRestrictedToken(api, currentToken, logonSid, writeSids, known, mode) {
	const restrictingSids = buildRestrictingSids(mode === "read-only" ? [logonSid, known.world] : writeSids.length === 0 ? (() => {
		throw new Error("createRestrictedToken: workspace-write restricting list requires at least one write SID");
	})() : [
		logonSid,
		known.world,
		...writeSids
	]);
	const tokenSlot = allocPtrSlot();
	if (api.createRestrictedToken(currentToken, 13, 0, null, 0, null, restrictingSids.length / 16, restrictingSids, tokenSlot) === 0) throwLastError$1(api, "CreateRestrictedToken", `restricting SIDs: ${restrictingSids.length / 16}`);
	...
}
```

**flags = `13` = `0xD`**，我已按位解码：

| 位 | 常量 | 含义 |
|---|---|---|
| `0x1` | `DISABLE_MAX_PRIVILEGE` | 剥离所有特权 |
| `0x4` | `LUA_TOKEN` | LUA/受限 |
| `0x8` | **`WRITE_RESTRICTED`** | **只对"写类"访问做第二遍交叉校验** |

限制性 SID 列表：

| mode | restricting SIDs |
|---|---|
| `read-only` | `[logonSid, Everyone]` — **不含任何 write SID** |
| `workspace-write` | `[logonSid, Everyone, workspaceSID, tempSID?]` |

**为什么必须有 `logonSid` + `Everyone` 保活组**（`:855-857`）：
> early DLL init dies with `0xC0000142` and CNG (`\Device\CNG` write trustee — pwsh crashes `0xE0434352`) fails without them.

**`Authenticated Users` 在两个列表里都被刻意剔除**（`:865-868`）：
> the WMI namespace security check fails (`0x80041003`), so **CIM is unavailable in every confined mode**, and the `C:\`-root tree-creation escape (standing `AU:(AD)` + `AU:(OI)(CI)(IO)(M)` ACEs) is closed in both.
> `INTERACTIVE`/`LOCAL` are absent from BOTH lists too — the host's Public tree grants write to INTERACTIVE.

> **这条解释了你的 `Get-CimInstance`/`Get-WmiObject` 在受限模式下不可用。**

令牌还要做两件事：

**(a) 降完整性级别到 Low**（`:833-840`）：

```js
function restrictTokenIntegrity(api, token, lowLabelSidPtr) {
	const sidLength = api.getLengthSid(lowLabelSidPtr);
	...
	info.writeUInt32LE(32, 8);
	if (api.setTokenInformation(token, 25, info, info.length) === 0) throwLastError$1(api, "SetTokenInformation", "TokenIntegrityLevel (Low)");
}
```
`TokenInformationClass 25` = `TokenIntegrityLevel`；`32` = `SE_GROUP_INTEGRITY`；SID 为 `makeWellKnownSid(api, 66)`（`WinLowLabelSid` = `S-1-16-4096`）。

**(b) 改写令牌默认 DACL**（`:801-823`）— **这是命名管道 EPERM 的解药，但不是病因**：

```js
function setTokenDefaultDaclGrant(api, token, sidPtr) {
	...
	const result = api.setEntriesInAclW(1, buildExplicitAccess(sidPtr, 1, FILE_ALL_ACCESS), currentDacl, newDaclSlot);
	...
	if (api.setTokenInformation(token, 6, info, info.length) === 0) { ... }
}
```
`TokenInformationClass 6` = `TokenDefaultDacl`，`FILE_ALL_ACCESS = 2032127 = 0x1F01FF`。

### 2.4 受限进程如何启动

**`CreateProcessAsUserW`** — 绑定见 `dsh-win32-process\lib\index.js:189-201`：

```js
createProcessAsUserW: bind(advapi32, "CreateProcessAsUserW", "int", [
    PVOID,        // hToken
    "str16",      // lpApplicationName
    "str16",      // lpCommandLine
    PVOID, PVOID, // lpProcessAttributes, lpThreadAttributes
    "int",        // bInheritHandles
    "uint32",     // dwCreationFlags
    PVOID,        // lpEnvironment
    "str16",      // lpCurrentDirectory
    koffi.pointer(STARTUPINFOW),
    koffi.pointer(PROCESS_INFORMATION)
]),
```

调用点（`index.js:425-427`）：

```js
function createRestrictedProcess(api, options, commandLine, creationFlags, startupInfo, processInfo) {
	return api.createProcessAsUserW(options.token, null, commandLine, null, null, 1, creationFlags, null, options.cwd, startupInfo, processInfo);
}
```

**明确结论**：
- **是 `CreateProcessAsUserW`**，**不是** `CreateProcessWithTokenW`。
- **没有 `STARTUPINFOEX`**，**没有 `PROC_THREAD_ATTRIBUTE_*`**（全树零命中）——所以不涉及 AppContainer 属性列表。
- 用的是古董 `STARTUPINFOW`（`encodeStartupInfo`，`cb: 104`、`dwFlags: 257` = `STARTF_USESTDHANDLES|STARTF_USESHOWWINDOW`）。
- **`lpEnvironment` 传 `null`**（第 7 个实参），原因见 README：*"an explicit block through Koffi trips `ERROR_INVALID_PARAMETER` in `CreateProcessAsUserW`"*。因此 TMP/TEMP 改写是**先 `SetEnvironmentVariableW` 改自己的环境**（`runner.js:154-155`）再让子进程继承。

两条 spawn 路径：
- `spawnSandboxedInherited()` → `spawnInheritedJobProcess()`（`stdio: 'inherit'`，kill-on-close Job，**先挂起创建 → 分配 Job → 恢复线程**，creationFlags `4` = `CREATE_SUSPENDED`）
- `spawnSandboxed()` → `spawnPipedProcess()`（匿名管道，creationFlags `0`）

### 2.5 ACL 如何拒绝工作区外的写入

`grantWrite()`（`:592-613`）在**一次** `SetNamedSecurityInfoW` 里下三条编辑：

```js
mergeAndApply(api, path, Buffer.concat([
    buildExplicitAccess(worldSidPtr, 3, 64, 2),   // Deny ACE: Everyone, FILE_DELETE_CHILD, 仅容器继承
    buildExplicitAccess(sidPtr, 1, GRANT_MASK)    // Allow ACE: 能力 SID, GRANT_MASK, OI|CI
]), oldAcl, { kind: "apply", acl: label }, descriptor, "grantWrite");
```

**(1) 能力 SID 的 Allow ACE —— `GRANT_MASK = 1114454`**

我已按位解码 `0x110156`：

| 位 | 名称 | 是否在掩码内 |
|---|---|---|
| `0x2` | `FILE_WRITE_DATA` | ✅ (= `W`) |
| `0x4` | `FILE_APPEND_DATA` | ✅ |
| `0x10` | `FILE_WRITE_EA` | ✅ |
| `0x100` | `FILE_WRITE_ATTRIBUTES` | ✅ |
| `0x40` | `FILE_DELETE_CHILD` | ✅ (= `DC`) |
| `0x10000` | `DELETE` | ✅ (= `D`) |
| `0x20000` | `READ_CONTROL` | ❌ 刻意排除 |
| `0x40000` | `WRITE_DAC` | ❌ **刻意排除** |
| `0x80000` | `WRITE_OWNER` | ❌ **刻意排除** |
| `0x100000` | `SYNCHRONIZE` | ✅ |

即 `GRANT_MASK` **恰好等于 `(OI)(CI)(W,D,DC)`**（继承标志 OI|CI 由 `buildExplicitAccess` 的 `inheritance=3` 给出）—— 与 README 里的 `(OI)(CI)(W,D,DC)` 描述**逐位吻合**。

`WRITE_DAC`/`WRITE_OWNER` 排除的理由（`:18-21`）：
> WRITE_DAC and WRITE_OWNER stay excluded so a confined child cannot rewrite DACLs or take ownership to escape the allowlist.

**(2) world-Deny ACE**：`buildExplicitAccess(worldSidPtr, 3, 64, 2)` → `mode=3`(`DENY_ACCESS`)、`mask=64`(`FILE_DELETE_CHILD`)、`inheritance=2`(仅 `OI`，容器)。

理由（`acl.d.ts:11-15`）：
> the deny is what keeps one granted root out of another's reach: Windows also authorizes a delete from the parent directory's `FILE_DELETE_CHILD` right, **which the token's write-restricted intersection does not reach**.

仅继承到容器：`0x40` 是 `FILE_ALL_ACCESS` 的成员，若继承到文件会否认根内每一次 `GENERIC_ALL` 打开。

**(3) Low 强制标签**（`buildLowLabelAcl`，`:425-442`）：

```js
if (api.initializeAcl(acl, aclLength, 2) === 0) { ... }
if (api.addMandatoryAce(acl, 2, 3, 1, lowLabelSidPtr) === 0) { ... }
```
`AddMandatoryAce(Acl, AceRevision=2, AceFlags=3(OI|CI), AceType=1(SYSTEM_MANDATORY_LABEL_ACE_TYPE), LabelSid=Low)` → **no-write-up 策略**，继承到子容器与对象。

**完整拒绝链条**（README:98 逐字）：
> Windows performs the access check **twice** — once against the normal SIDs, once against the restricting SIDs — and grants write-class access **only where both checks pass**; separately, the kernel's mandatory-integrity check denies write-class access to every object that is not labeled Low.

所以工作区外被拒有**三层**叠加：① 普通 SID 遍可能过但 ② restricting SID 遍无 write SID 匹配 → 交叉失败；③ 即便两者都过，非 Low 标签对象仍被完整性检查挡住。**`read-only` 靠的是"列表里不装 write SID"**，因此早先 `workspace-write` 期间留下的标准 ACE **依然惰性失效**（`:857-863`）。

### 2.6 grants 的生命周期与并发

- **workspace grant 是 STANDING（永不撤销）**：`grant.add(workspaceRoot, true)`（`sandbox-local\lib\index.js:402`）→ 跨会话复用缓存。撤销会迫使下个会话重传播整棵树。
- **temp grant 是 REVOCABLE**：`dispose()` 调 `revokeWrite()`。
- **幂等跳过**：`hasExactGrant && hasExactDeny && hasExactLabel` 三者齐全则**跳过** `SetNamedSecurityInfoW`（否则会在大工作区上做"急切全树继承传播"，耗时以分钟计）。
- **并发保护**：`withPathLock()`（`:361-387`）用 `LockFileEx` 对每路径排他锁，锁文件在 `<GetTempPathW()>\dsh-acl-locks\<sha256(小写路径)前16位>.lock`。`CreateFileW` 参数 `-1073741824`(=`GENERIC_READ`)、`3`(`FILE_SHARE_READ|WRITE`，**刻意不含 DELETE**)、`4`(`OPEN_ALWAYS`)。

### 2.7 runner argv 契约

`runner.js:16-19` 注释逐字：

```
[node, runner.js, '--workspace', <dir>, '--temp', <dir>,
 '--mode', <read-only|workspace-write>,
 ['--write-sid', <S-1-4-…>,
  '--temp-write-sid', <S-1-4-…>], '--', <argv...>]
```

- 有 `--write-sid`/`--temp-write-sid` ⇒ `seamManaged = true` ⇒ `manageDacls: false`，runner **不授权也不撤销**（由调用方 `LocalSandboxProvider` 持有 grant）。
- 无这对参数（独立/无 agent 场景）⇒ runner 自己 `mkdtempSync(join(parsed.temp, "dsh-"))` 造私有 temp、自派生 SID、退出时删除。
- 失败契约：任何 runner 侧失败 → stderr 打印 `windows-acl-run: <detail>` 并 **exit 127**，**子进程绝不无限制启动**。

### 2.8 已声明的边界（`types-Cl_DXjhk.js:1168-1170`）

> - **writes are restricted; reads, network, and process visibility are NOT** (`WRITE_RESTRICTED` intersects only write accesses);
> - **console isolation is unavailable** — `CREATE_NO_WINDOW`/`CREATE_NEW_CONSOLE` children die with `STATUS_DLL_INIT_FAILED` under the restriction;
> - the private temp directory and every writable directory must be owned by the caller.

以及 README:186-189 的硬链接逃逸与 AppContainer package SID 不可读问题：

> - **Hard links are file-object aliases** — an inheritable workspace grant propagated onto an existing hard link labels and grants the one underlying file security descriptor, so the same object is writable through an external alias.
> - a file whose DACL carries an ACE for a package SID (`S-1-15-2-…`) is **inaccessible to a Low-integrity token** on this host, even when the same DACL grants the user full control and Everyone read.

---

## 3. 非 Windows 实现

### 3.1 平台链选择

`dsh-sandbox-local\lib\index.js:173-177`：

```js
const PLATFORM_CHAINS = {
	linux: ["bwrap", "landlock"],
	darwin: ["seatbelt"],
	win32: ["windows-acl"]
};
```

**先按平台选链，再看探测**：链只有**一个**候选时**不探测**直接选中（`chainVerdict()`，`:496-512`）；多候选才按顺序做功能探测仲裁。无链或全不通 → `SandboxUnavailableError`，**失败即关闭**。

`STATIC_ENFORCEMENT`（`:187-192`）：`bwrap: "full"`、`landlock: "full"`、`seatbelt: "full"`、**`"windows-acl": "partial"`**。

### 3.2 macOS / Seatbelt

`seatbeltProfileArgs()`（`:65-75`）：

```js
function seatbeltProfileArgs(policy) {
	const forms = [
		"(version 1)",
		"(allow default)",
		"(deny file-write*)",
		`(allow file-write* (literal ${sbplString("/dev/null")}))`
	];
	const roots = writableRoots(policy);
	if (roots.length > 0) forms.push(`(allow file-write* ${roots.map((root) => `(subpath ${sbplString(root)})`).join(" ")})`);
	return ["-p", forms.join(" ")];
}
```

调用形式 `sandbox-exec -p "<SBPL>" -- <argv...>`（`seatbeltExec()` 默认 `"sandbox-exec"`，`:529`）。策略：**`(allow default)` 全放行，然后 `(deny file-write*)` 全禁写，再按 `(subpath ...)` 逐个开洞**（白名单式，非黑名单）。

### 3.3 Linux / bwrap

`bwrapProfileArgs()`（`:22-39`）：

```js
const args = [
    "--ro-bind", "/", "/",
    "--dev", "/dev",
    "--unshare-pid",
    "--proc", "/proc",
    "--die-with-parent"
];
if (policy.mode === "workspace-write") {
    args.push("--tmpfs", "/tmp");
    args.push("--bind", policy.workspaceRoot, policy.workspaceRoot);
}
```

`read-only` ⇒ 只有 `--ro-bind /`，无任何可写 bind。`workspace-write` ⇒ 额外 `--tmpfs /tmp`（**临时且丢弃**，与 Windows/文件栅栏的语义差异被显式记录）+ `--bind <workspaceRoot>`。

### 3.4 Linux / Landlock

**不是** DSH 自己写的原生插件——来自 `@deepseek-ai/node-addon-system/landlock-run` 的 `launcherPath()`：

`landlockProfileArgs()`（`:45-52`）：

```js
const readWrite = ["/dev/null"];
if (policy.mode === "workspace-write") readWrite.push("/tmp", policy.workspaceRoot);
return grantArgs({ readOnly: ["/"], readWrite });
```

调用形式 `[launcher, ...grantArgs(...), "--", ...argv]`。失败契约独有（`RUNNER_FAILURE_RULES.landlock`，`:233-237`）：

```js
landlock: [{
    allowedExitCodes: [LAUNCHER_FAILURE_EXIT],
    fatalSignatures: [`${LAUNCHER_BIN}: `],
    informationalLines: [`${LAUNCHER_BIN}: partial enforcement (older Landlock ABI)`]
}]
```
即有**版本化的 exit-125** 加致命行契约，且 launcher 每次受限运行都会在 stderr **自报 partial enforcement**。

### 3.5 各后端的 denial 方言

`DENIAL_SIGNATURES`（`:206-217`）：

```js
const DENIAL_SIGNATURES = {
	bwrap: ["read-only file system"],
	landlock: ["permission denied"],
	seatbelt: ["operation not permitted"],
	"windows-acl": ["access is denied", "access to the path", "permission denied", "operation not permitted"],
	runnerCommand: ["read-only file system", "permission denied"]
};
```

### 3.6 danger-full-access 如何绕过

`SandboxPwshExecutor.execute()`（`dsh-pwsh-sandbox\lib\index.js:151-160`）：

```js
async execute(spec) {
    const policy = spec.sandboxPolicy;
    const { mode } = policy;
    if (mode === "danger-full-access") return SandboxPwshExecutor.decorateResult(await super.execute(spec), (result) => ({
        ...result,
        sandbox: { mode, denied: false }
    }));
    ...
```
即**根本不调用 `confine()`**，直接走父类本地执行。fs 侧同理（`checkedTarget` 首行 `if (mode === "danger-full-access") return target;`）。

---

## 4. 文件工具（write/edit/read）如何被围栏

包 `@deepseek-ai\dsh-fs-sandbox`，`lib\index.js`（169 行）。

### 4.1 标识符与挂载方式

**`SandboxedFileSystem` 确实存在**（`class SandboxedFileSystem extends LocalFileSystem`，`:103`）。注册为 **`ctx.fs`**：

> Registers as `ctx.fs` (loading it **INSTEAD OF** `dsh-fs-local`, together with a `ctx.sandboxPolicy`, is the whole swap — the model-facing tools are untouched).

它**继承全部本地文本存储机制**（resolve/stat/read/stream/list/原子写/read-match-write 编辑临界区），只**新增两个突变上的策略栅栏**：

```js
async writeText(target, content, expected, signal, sandboxPolicy) {
    return super.writeText(await this.checkedTarget(target, sandboxPolicy), content, expected, signal);
}
async editText(target, edit, expected, signal, sandboxPolicy) {
    return super.editText(await this.checkedTarget(target, sandboxPolicy), edit, expected, signal);
}
```

### 4.2 **read 完全不过栅栏**

`:74-75` 逐字：
> **Reads pass through untouched: every mode permits reading.**

**所以 `read` 工具在任何 mode 下都可读任意路径**——这与 Windows 侧 `WRITE_RESTRICTED` 只交叉校验写访问完全一致。**读取隔离不在 DSH 沙箱的承诺范围内。**

### 4.3 路径校验：realpath + 符号链接 + 大小写

`checkedTarget()`（`:153-166`）逐字：

```js
async checkedTarget(target, sandboxPolicy) {
    const policy = sandboxPolicy ?? this.ctx.sandboxPolicy.resolve();
    const { mode } = policy;
    if (mode === "danger-full-access") return target;
    if (mode === "read-only") throw new FsError(`cannot write "${target.displayPath}": file access denied under read-only mode`, "FS_SANDBOX_DENIED");
    const fresh = await this.resolve(target.displayPath);
    let contained = false;
    for (const root of writableRoots(policy)) if (await isPathUnder(fresh.targetKey, root)) {
        contained = true;
        break;
    }
    if (!contained) throw new FsError(`cannot write "${target.displayPath}": file access denied under workspace-write mode`, "FS_SANDBOX_DENIED");
    return fresh;
}
```

- **用 realpath**：`this.resolve(target.displayPath)` 会 realpath 最深的已存在祖先——**在授权判定前重新规范化，以反映并发被换掉的符号链接**。
- **检查符号链接**：是，通过 realpath 间接实现。注释（`:83-85`）坦承残余 TOCTOU：*"an ancestor symlink swapped between the containment re-check and the syscall"* 被**收窄但未消除**，且**该威胁模型下接受**。
- **返回值即被写入的目标**（`return fresh`），刻意消除 "这里检查、那里写" 的 TOCTOU。

**`isPathUnder()`（`:53-65`）双层判定**：
1. **词法快速路径** `isLexicallyUnder()`：`caseSensitive` 默认 `process.platform !== "win32"` ⇒ **Windows 上大小写不敏感**（两边 `toLowerCase()`），比较时要加 `sep` 前缀。
2. **回退路径**：词法不匹配时，**逐级向上 walk 目标的已存在祖先**，用 `sameIdentity()`（`dev === dev && ino === ino`，`stat(..., {bigint:true})`）比对文件系统身份——**这是为了识别 Windows 长名/8.3 别名与大小写差异，而不把包含判定弱化成文本近似**。

### 4.4 错误码

**`FS_SANDBOX_DENIED`** —— 确认存在，由 `FsError` 抛出（两条 throw 各一次）。

这**不是**模型看到的东西。工具层把它映射为 `dsh-sandbox\lib\index.js:64-66` 的标记：

```js
function sandboxDenialMarker(mode) {
	return `[sandbox: file access denied under ${mode} mode]`;
}
```

**`FS_SANDBOX_DENIED` 是给工具层结构化消费的 code**，`[sandbox: ...]` 才是喂给模型的文本。

### 4.5 定位说明（重要）

`:79-82` 逐字：
> The fence is a policy check in **TRUSTED code** over a **MODEL-CONTROLLED path**, **NOT a kernel boundary** ... This is containment, not a security boundary; kernel-grade isolation of untrusted CODE stays `ctx.shell`'s job.

即：**fs 围栏防的是"模型给的路径"，不是"不受信任的代码"。** 真正的内核级边界只在 `ctx.shell`（bash/pwsh）那一侧。

---

## 5. 审批 / 权限模式如何交互

### 5.1 `DSH_PERMISSION_MODE` 的作用

全树**只有 2 处命中**，都在 `dsh-base\cordis.patch.yml`：

```yaml
# :232  沙箱模式
mode: !!js process.env.DSH_PERMISSION_MODE ?? 'workspace-write'

# :248  审批策略
policy: !!js "(process.env.DSH_PERMISSION_MODE ?? 'workspace-write') === 'danger-full-access' ? 'never' : 'ask'"
```

所以 `DSH_PERMISSION_MODE` **同时**驱动沙箱 mode 和 approval policy，是一个"部署级环境变量覆盖"。

### 5.2 审批策略的取值

`dsh-user-approval\lib\index.js:37`：

```js
const APPROVAL_POLICIES = ["ask", "never"];
```

**只有两个值**（不是 `ask/never/etc.`，没有第三个）。Config schema（`:74`）：

```js
static Config = z.object({ policy: z.union(["ask", "never"]).default("ask") });
```

- **`ask`**：委托给已组合的应答者（answerers）；**无应答者时失败即关闭 → `unavailable`**。
- **`never`**：在 waterfall 分发**之前**确定性拒绝每个请求 → `'rejected'`。

**outcome 词汇表**（封闭）：`allowed-once` / `rejected` / `cancelled` / `unavailable`。**只有一次性授权，没有 `allow-always`、没有记住规则、没有授权存储。**

`effectivePolicy()`（`:153`）：**会话自己的 `approval/policy` fold ?? 配置默认**。

### 5.3 沙箱 mode 与 approval policy 的耦合 ——【用户笔记的论断：**属实**】

**已在代码中直接核实。** 两处独立证据：

**证据 A** — `dsh-base\cordis.patch.yml:245-248`：

```yaml
- id: approval
  name: '@deepseek-ai/dsh-user-approval'
  config:
    policy: !!js "(process.env.DSH_PERMISSION_MODE ?? 'workspace-write') === 'danger-full-access' ? 'never' : 'ask'"
```

**证据 B** — `dsh-base\cordis.patch.yml:250-262`，预设表把两者**成对**定义：

```yaml
- id: permission
  name: '@deepseek-ai/dsh-permission-presets'
  config:
    presets:
      read-only:
        sandbox: read-only
        approval: ask
      workspace-write:
        sandbox: workspace-write
        approval: ask
      danger-full-access:
        sandbox: danger-full-access
        approval: never
```

**结论**：设 `DSH_PERMISSION_MODE=danger-full-access` **确实会把 approval policy 变成 `never`**。而 `never` 在服务内部、waterfall 分发之前就被决断（`:173-176` 注释：*"The `never` policy is decided HERE, before any dispatch ... so a documented promise that `'never'` rejects deterministically regardless"*），**后注册且 `prepend` 的监听器也无法绕过**。

⇒ **pieqi 的审批卡链路会彻底断掉**。你的 `cordis.patch.yml` 注释里那句判断（"那会把 approval policy 连带改成 never，pieqi 的人工审批卡会彻底消失"）**完全正确**。

### 5.4 升级（escalation）机制

`dsh-sandbox\lib\index.js:30-42`：

```js
const WIDER_MODES = {
	"read-only": ["workspace-write", "danger-full-access"],
	"workspace-write": ["danger-full-access"]
};
const ESCALATION_TARGETS = ["workspace-write", "danger-full-access"];
```

`approveEscalation()`（`:99-123`）：**严格更宽**校验 → 必须经审批 → **只对这一条命令生效**（`allowed-once`）。参数配对强制：`sandbox_permissions` 与 `justification` **必须同时出现**，且 `justification` 非空（`:51-55`）。

---

## 6. 只读 pwsh 的 ConstrainedLanguage ——【属实，但机制出乎意料】

### 6.1 结论

**没有找到任何设置 `ConstrainedLanguage` / `__PSLockdownPolicy` / `PSModulePath` / WDAC 的代码。** 这些标识符（除文档提及外）在实现代码中**零命中**。

**DSH 并不主动设置语言模式。** 这是 **Windows ACL 限制的副产物**。

### 6.2 README 的权威解释

`dsh-sandbox-windows-acl\README.md:192` 逐字：

> **PowerShell language mode differs by confined mode** — under `read-only`, PowerShell **cannot create its AppLocker probe files in temp** and conservatively starts in **ConstrainedLanguage** (`Add-Type`, non-core .NET static calls, COM, and reflection fail); the shipped `workspace-write` path **lets the probe complete**, so pwsh stays in **FullLanguage** unless host-wide WDAC/AppLocker policy says otherwise, while a direct `AclSandbox` with `tempDir: null` has no such guarantee. **This split is PowerShell startup behavior, not part of the ACL write boundary.**

`dsh-tool-pwsh\README.md:205` 补充：

> read-only pwsh starts in ConstrainedLanguage because **its temp write denial makes PowerShell's AppLocker probe fail closed** ... **and the mode cannot be lifted from inside**.

### 6.3 精确机制

| mode | temp 目录 | PowerShell AppLocker 探测 | 语言模式 |
|---|---|---|---|
| `read-only` | **无**（`--temp` 传 ambient `tmpdir()`，且**不授予任何 write SID**） | **失败**（无法在 temp 建探测文件） | **ConstrainedLanguage** |
| `workspace-write` | 私有随机目录 + **独立 temp write SID 授权** | **成功** | **FullLanguage**（除非宿主 WDAC/AppLocker 另有规定） |

**链条**：`read-only` ⇒ restricting 列表不含 write SID ⇒ temp 写入被拒（`access is denied`） ⇒ PowerShell 启动时无法创建 AppLocker 探测文件 ⇒ PowerShell **保守地自我降级**到 ConstrainedLanguage。

**这是 PowerShell 自身的安全行为被 DSH 的 ACL 边界意外触发**，不是 DSH 的功能。工具描述里那句 "the mode cannot be lifted from inside" 即指无法从不受限的命令内部解除。

**副作用**（`dsh-tool-pwsh-persistent\README.md:154`）：
> which may deny the bootstrap's `[Console]::` encoding pin and prompt marker. Commands can still settle through the printable prompt and silence tier, but non-ASCII output may follow the host code page.

⇒ **只读模式下非 ASCII 输出可能跟随宿主代码页**。

**【未能确定】**：ConstrainedLanguage 的确切判定是 PowerShell 内部（`System.Management.Automation`）的 AppLocker/WDAC 探测逻辑，其源码不在本机安装范围内，**我无法在本机代码中进一步证实探测文件的确切路径与判别式**。README 与工具描述是权威来源。

---

## 7. 命名管道 / 管道 stdio 限制

### 7.1 现象与结论

工具描述（`dsh-tool-pwsh\lib\index.js:257`）逐字：

> In both confined modes, programs cannot open named pipes, so a command that captures another program's output through piped stdio (Node.js `child_process.spawn`/`exec` with the default `stdio: 'pipe'`) **fails with EPERM**, while `stdio: 'inherit'` and `stdio: 'ignore'` spawns work, and **PowerShell's own pipelines are unaffected**.

### 7.2 精确原因

`dsh-sandbox-windows-acl\README.md:186` 逐字：

> **Piped stdio capture is impossible for confined grandchildren** — **libuv's pipe stdio uses named pipes**, whose client-end open requests **write access no restricting SID is granted (the Win32 layer's default SD template, not the token default DACL)**, so `spawn(..., { stdio: 'pipe' })` inside a confined process fails with EPERM; inherited and ignored stdio spawns work, and **anonymous pipes (PowerShell pipelines) work because the restricted token's default DACL carries a full-access restricting-SID ACE**.

### 7.3 本机代码中的对应实现

**(a) DSH 自己造匿名管道时传 `null` 安全属性** —— `dsh-win32-process\lib\index.js:392-397`：

```js
function createPipe(api, owned) {
	const readSlot = allocPtrSlot();
	let writeSlot;
	try {
		writeSlot = allocPtrSlot();
		if (api.createPipe(readSlot, writeSlot, null, 0) === 0) throwLastError(api, "CreatePipe");
```

`CreatePipe(ppReadPipe, ppWritePipe, lpPipeAttributes = null, nSize = 0)` —— **`lpPipeAttributes` 传 `null`** ⇒ 管道句柄**不可继承**、且用默认安全描述符。

**(b) 但让子进程能写自己的 stdio 的是句柄继承 + 令牌默认 DACL**（`:447`）：

```js
]) if (api.setHandleInformation(handle, 1, 1) === 0) throwLastError(api, "SetHandleInformation", label);
```
`SetHandleInformation(handle, HANDLE_FLAG_INHERIT=1, HANDLE_FLAG_INHERIT=1)` 临时把 stdio 句柄设为可继承。

### 7.4 为什么 grandchild 的 named pipe 会 EPERM

关键区分：**DSH 自己造的匿名管道 vs. 受限子进程内部再 spawn 时 libuv 造的命名管道**。

1. 受限令牌的 **restricting SID 列表**里只有 `logonSid` + `Everyone`（+ write SID），**`Everyone` 对新建命名管道对象的写权限并不成立**。
2. Node/libuv 的 `stdio: 'pipe'` 走 **Windows 命名管道**（`\\.\pipe\...`），**不是** DSH 用的 `CreatePipe` 匿名管道。
3. 命名管道**客户端端的打开请求需要写访问**，而：
   - **没有**任何 restricting SID 被授予该写权限；
   - 该权限来自 **Win32 层的默认安全描述符模板**，**不是**令牌的默认 DACL —— 所以 §2.3(b) 那次 `setTokenDefaultDaclGrant` **救不了它**（README 明说 "not the token default DACL"）。
4. `WRITE_RESTRICTED` 的交叉校验 ⇒ 写类访问失败 ⇒ `ERROR_ACCESS_DENIED` ⇒ **Node 把它暴露为 `spawn EPERM`**。

**为什么 `inherit`/`ignore` 可以**：它们不新建命名管道对象，直接复用**已继承**的句柄（继承在创建时已由 `SetHandleInformation` + `bInheritHandles=1` 完成，不需要在管道对象上过访问检查）。

**为什么 PowerShell 自己的管道不受影响**：PowerShell 的 `|` 用**匿名管道**，而受限令牌的默认 DACL 被 `setTokenDefaultDaclGrant` 加了**全访问 restricting-SID ACE**（§2.3(b)）⇒ 新建的匿名管道自身 DACL 能通过第二遍校验。`setTokenDefaultDaclGrant` 的 `.d.ts` 注释（`token.d.ts:36-50`）正是为此：

> The restricted token inherits the user's default DACL verbatim, which names no restricting SID: **a new anonymous pipe (child stdio) therefore fails the write pass-2 check at creation (ERROR_ACCESS_DENIED; Node surfaces it as spawn EPERM), breaking every piped-stdio grandchild spawn.** The merged ACE names a RESTRICTING SID ..., so each new object's own DACL passes pass-2 ...

> ⚠️ **该注释描述的是修复"DSH 自己的匿名管道"问题**，而命名管道问题（§7.4）来自**另一套默认 SD 模板**，是**已知且未修复**的边界。

---

## 8. `allowedDirs` / `extraRoots` 与"平台临时区域也可写"

### 8.1 官方**没有** `allowedDirs`

**【重要】** `allowedDirs` 在**整个 `@deepseek-ai` 官方树中零命中**。官方 `SandboxPolicyService.Config` 只有 `mode` + `workspaceRoot`。

本机配置里的 `allowedDirs` **完全来自第三方插件** `dsh-sandbox-allowlist@0.2.0-beta.1`。

### 8.2 插件替换了哪些官方 API

`C:\Users\13631\.dsh\profiles\acp\node_modules\dsh-sandbox-allowlist\cordis.patch.yml` 逐字：

```yaml
- id: sandbox-policy
  disabled: true
- id: fs-sandbox
  disabled: true
- id: sandbox
  disabled: !!js process.platform !== 'win32'
- insert:
    - id: sandbox-allowlist-policy
      name: 'dsh-sandbox-allowlist'          # → lib/policy.mjs
      config:
        mode: !!js process.env.DSH_PERMISSION_MODE ?? 'workspace-write'
        workspaceRoot: !!js process.cwd()
    - id: sandbox-allowlist-fs
      name: 'dsh-sandbox-allowlist/lib/fs.mjs'
    - id: sandbox-allowlist-provider
      name: 'dsh-sandbox-allowlist/lib/provider.mjs'
      disabled: !!js process.platform === 'win32'
```

**插件做了什么**（README 自述）：

| 官方服务 | 插件替换物 | 手段 |
|---|---|---|
| `sandboxPolicy` (`SandboxPolicyService`) | `AllowlistPolicyService extends SandboxPolicyService` | **子类化** + 覆写 `resolve()` |
| `fs-sandbox` (`SandboxedFileSystem`) | `lib/fs.mjs` | 子类化，覆写 `checkedTarget()` |
| `sandbox` (`LocalSandboxProvider`) | `AllowlistSandboxProvider extends LocalSandboxProvider` | 子类化，覆写 `confine()`（**仅 Linux**） |

逐字确认：

- **policy.mjs:120** `export class AllowlistPolicyService extends SandboxPolicyService {`
- **policy.mjs:296-304** 覆写 `resolve()`，**调用 `super.resolve(request)` 后**注入非官方字段 `policy.extraRoots`：
  ```js
  resolve(request = {}) {
    const policy = super.resolve(request)
    if (policy.mode === 'workspace-write' && this._currentPatterns().length > 0) {
      const roots = this.expandRoots()
      if (roots.length > 0) {
        policy.extraRoots = roots
        if (acl !== null) this._ensureGrants(policy.workspaceRoot, roots)
      }
    }
    ...
  ```
  **`extraRoots` 不是官方字段** —— 是插件私有契约。
- **fs.mjs:58-70** 覆写 `checkedTarget()`，把 `policy.extraRoots` 并入包含判定。
- **provider.mjs:24** `export class AllowlistSandboxProvider extends LocalSandboxProvider {`，覆写 `confine()`。**Windows 上这个 provider 行被 `disabled`** —— 因为 Windows 白名单走的是 ACL 授权（`policy.mjs` 里的 `_ensureGrants` 直接调官方 `@deepseek-ai/dsh-sandbox-windows-acl` 的 ACL API），**不需要换 provider**。

**插件复用了官方导出**（policy.mjs:37、:118）：
```js
import { SandboxPolicyService } from '@deepseek-ai/dsh-sandbox-policy'
const acl = process.platform === 'win32' ? await import('@deepseek-ai/dsh-sandbox-windows-acl') : null
```
即 `AclWriteGrant` / `workspaceWriteSid` / `tempWriteSid` —— **官方公开 API，非 monkey-patch**。

**注意**：patch 是**整体替换** config，所以插件必须复述 `mode`/`workspaceRoot`（你的 `cordis.patch.yml` 注释已经指出这点）。

### 8.3 "平台临时区域也可写"的来源

**官方函数 `writableRoots()`** —— `dsh-sandbox\lib\index.js:166-173`：

```js
function writableRoots(policy) {
	if (policy.mode !== "workspace-write") return [];
	return [...new Set([
		policy.workspaceRoot,
		"/tmp",
		tmpdir()
	].map(canonicalPath))];
}
```

**逐字注释**（`:157-165`）：
> `read-only` allows nothing; `workspace-write` allows **the policy's workspace root, the host `/tmp`, and the per-user platform temp dir (`os.tmpdir()` — the real temp area for mkstemp-family tools; omitting it would deny what the mode promises)**.

`canonicalPath()`（`:150-156`）用 `realpathSync.native()`，失败则保持原样（**"缺失的根匹配不到任何东西"**，保守）。

**三方共享此定义**（`:126-138` 模块注释）：
> The Seatbelt profile (`dsh-sandbox-local`) and the in-process filesystem fence (`dsh-fs-sandbox`) both derive their allow-list here, so **"the write tool cannot write /tmp but bash can" asymmetries cannot arise between them.**

⇒ 系统提示里那句 "Some platform temporary areas may also be writable." **精确对应 `writableRoots()` 的 `/tmp` + `os.tmpdir()`**。

**`read-only` 返回空数组** ⇒ 什么都不可写。

### 8.4 官方 Windows 侧的 temp 处理（与 `writableRoots` 的差异）

**注意**：Windows 上**没有**把 `os.tmpdir()` 整个授成可写。看 `LocalSandboxProvider.windowsAclRunnerArgv()`（`sandbox-local\lib\index.js:357-382`）：

- **无 session 或 `read-only`** ⇒ `--temp tmpdir()` 且**不带 SID 参数** ⇒ runner 在 ambient temp 下**自建随机私有子目录**，为该次调用独占。
- **`workspace-write` + 有 session** ⇒ `materializeAclGrant()` 用 `mkdtempSync(join(tmpdir(), "dsh-"))` 造**每会话私有随机目录**，派生**独立** temp SID 并授权。

`materializeAclGrant()` 里的强制约束（`:397`）：
```js
assertTempRootOutsideWorkspace(workspaceRoot, tmpdir());
```
（`path-boundary.js:660-662`）理由：*"every child created below it would inherit the standing workspace capability"*。

且在 `AclSandbox.init()` 里 `assertPrivateTempDisjoint(this.writableDirs, tempDir)`（`:1270`），拒绝私有 temp 与任何可写目录**任一方向**的重叠。

⇒ **Windows 侧 temp 的可写性被刻意收窄到"每会话一个随机私有子目录"**，而非整个 `os.tmpdir()`。这是 Windows 与 POSIX 的**真实语义差异**。

### 8.5 你本机 `allowedDirs` 的实际效果

你的 `C:\Users\13631\.dsh\profiles\acp\cordis.patch.yml` 配了 5 个目录（Go/npm 缓存），插件在 Windows 上通过 `_ensureGrants()` 直接给它们**材料化官方 ACL ACE**。所以：

- **确属扩展**：这些根**不在**官方 `writableRoots()` 里（官方只有 workspaceRoot + `/tmp` + `tmpdir()`）。
- **生效层级**：在**文件写边界**（ACL + fs 栅栏）上追加，**不动** `approval` policy ⇒ pieqi 审批卡不受影响。**这正是你注释里写的设计意图，且与代码一致。**
- **插件自述的注意事项**（provider.mjs:13-15）：**landlock / seatbelt 的 profile builder 是模块私有的，无法从外部扩展** —— 只有 bwrap 支持。Windows 走 ACL 路线不受此限。

---

## 9. 运行时上下文与本机现状的交叉核对

你当前 runtime context 说：**`Current DSH file policy: workspace-write`**，且授权根包含：

```
C:\Users\13631\AppData\Local\go-build
C:\Users\13631\go\pkg\mod
C:\Users\13631\AppData\Local\npm-cache
E:\appData\AICache\.npm
```

- 前 3 个 + `.npm` 可对应到你 `cordis.patch.yml` 的 `allowedDirs`（`_ensureGrants` 材料化）。
- **`allowedDirs` 列表里有 `C:\Users\13631\.npm` 与 `C:\Users\13631\go\bin`，但 runtime context 显示的是 `E:\appData\AICache\.npm`** —— 说明 `.npm` 很可能是**符号链接/联接**，被 `canonicalPath()`（`realpathSync.native`）解析到了 E 盘。**这与 §4.3 的 canonical 语义一致。**
- `C:\Users\13631\go\bin` 未出现在授权根中 —— 可能该目录不存在（`canonicalPath` 解析失败时保留原样，而 `expandTrustedRoots` 可能跳过不存在的路径）。**【未能确定】**具体跳过逻辑未逐行核对 `patterns.mjs`。

---

## 10. 代码可判定性总结

### 【已核实，逐字引证】
1. mode 三值枚举、默认 `read-only`、`resolve()` 优先级、系统提示词文案
2. Windows：`CreateRestrictedToken(flags=13=DISABLE_MAX_PRIVILEGE|LUA_TOKEN|WRITE_RESTRICTED)`、`CreateProcessAsUserW`、`STARTUPINFOW`（**无 STARTUPINFOEX**）、无 AppContainer
3. SID 由 **sha256 字符串拼接**（**非 `AllocateAndInitializeSid`**）+ `ConvertStringSidToSidW` 解析；已独立复现
4. **`GRANT_MASK = 1114454 = 0x110156` 按位 = `(W,D,DC)`**，排除 `WRITE_DAC`/`WRITE_OWNER`；外加 world `FILE_DELETE_CHILD` Deny 与 Low 强制标签
5. **`danger-full-access` ⇒ approval `never`**（`dsh-base\cordis.patch.yml:248` 与预设表 `:250-262` 双重证据）→ **你的笔记属实**
6. fs 栅栏：`SandboxedFileSystem extends LocalFileSystem`，注册为 `ctx.fs`，错误码 **`FS_SANDBOX_DENIED`**，模型可见标记 `[sandbox: file access denied under <mode> mode]`
7. 路径校验：realpath 重规范化 + 词法/`dev:ino` 身份双层判定 + Windows 大小写折叠；**read 完全不过栅栏**
8. bwrap/landlock/seatbelt 的完整 argv 与 SBPL profile
9. `writableRoots()` = workspaceRoot + `/tmp` + `tmpdir()`；`read-only` 返回 `[]`
10. ConstrainedLanguage 的 README 权威解释（temp 写入被拒 → AppLocker 探测失败 → PowerShell 自降级）
11. 命名管道 EPERM 的 README 权威解释（Win32 默认 SD 模板，非令牌默认 DACL）+ 本机 `CreatePipe(..., null, 0)` 佐证
12. 插件以**子类化 + 覆写**替换 `sandboxPolicy`/`fs-sandbox`/`sandbox`，注入私有 `extraRoots`；Windows 上 provider 行被禁用
13. `PlatformChains`、`STATIC_ENFORCEMENT`（windows-acl = **partial**）、`DENIAL_SIGNATURES`、`RUNNER_FAILURE_RULES`

### 【未能确定】
1. **PowerShell AppLocker 探测的确切文件路径与判别式** —— 属 PowerShell 内部实现，不在本机安装范围内
2. **`patterns.mjs` 的 `expandTrustedRoots` 对不存在路径的完整处理**（用于解释 `go\bin` 缺失）—— 未逐行核对
3. **`_ensureGrants` 授予 `allowedDirs` 时是否递归全树** —— 从你的注释推断是（"急切的全树继承式 ACE 传播"），但未逐行核对 policy.mjs:170-280
4. **`E:\appData\AICache\.npm` 与 `C:\Users\13631\.npm` 的联接关系** —— 未验证文件系统 reparse point

### ⚠️ 与任务简报的**事实纠正**
- 简报示例提到 `allowedDirs` 是配置 schema 字段 —— **官方 schema 中没有**；它由第三方插件在 `extends SandboxPolicyService` 的子类中新增，官方对应概念是 `writableRoots()` 派生的固定三元组。
- 简报把 `SandboxedFileSystem` / `LocalSandboxProvider` / `SandboxPolicyService` 并列为"官方 API" —— **这三个标识符均真实存在**，已逐一确认。第三方的 `dsh-sandbox-allowlist` 通过**继承并覆写**（而非 monkey-patch）扩展它们。

---

## 附：关键文件清单

```
D:\program_dev\others\node\node.v20\node_modules\@deepseek-ai\dsh\node_modules\@deepseek-ai\
├─ dsh-sandbox\lib\index.js                       289 行  SandboxProvider 抽象、WIDER_MODES、
│                                                          sandboxDenialMarker、writableRoots、canonicalPath
├─ dsh-sandbox-policy\lib\index.js                159 行  SandboxPolicyService、SANDBOX_MODES、resolve()
├─ dsh-sandbox-local\lib\index.js                 556 行  LocalSandboxProvider、PLATFORM_CHAINS、
│                                                          bwrap/landlock/seatbelt profile、DENIAL_SIGNATURES
├─ dsh-sandbox-windows-acl\lib\types-Cl_DXjhk.js 1411 行  ★ ACL/令牌/SID/ACL 三元组全部实现
├─ dsh-sandbox-windows-acl\lib\runner.js          188 行  argv 前缀 runner、exit 127 契约
├─ dsh-fs-sandbox\lib\index.js                    169 行  SandboxedFileSystem、checkedTarget、isPathUnder
├─ dsh-pwsh-sandbox\lib\index.js                  252 行  SandboxPwshExecutor、danger-full-access 短路
├─ dsh-user-approval\lib\index.js                 ~180 行  APPROVAL_POLICIES=["ask","never"]
├─ dsh-win32-process\lib\index.js                 761 行  CreateProcessAsUserW/CreatePipe 绑定
├─ dsh-base\cordis.patch.yml                      529 行  ★ :232 / :248 / :250-262 环境变量与预设表
└─ dsh-tool-pwsh\lib\index.js                     257 行  工具描述（ConstrainedLanguage/命名管道契约）

C:\Users\13631\.dsh\profiles\acp\
├─ cordis.patch.yml                                    你的 allowedDirs 配置
└─ node_modules\dsh-sandbox-allowlist\lib\
   ├─ policy.mjs   415 行  AllowlistPolicyService extends SandboxPolicyService
   ├─ fs.mjs         ~75 行  checkedTarget 覆写，合并 extraRoots
   └─ provider.mjs   61 行  AllowlistSandboxProvider（Windows 上禁用）
```

**本报告未修改任何文件。**
