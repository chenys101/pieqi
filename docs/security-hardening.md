# Pieqi 外网安全整改方案

> 覆盖两种部署形态：**有域名（named 固定域名）** 与 **无域名（quick 随机域名 / 纯内网）**
> 版本：2026-10-02 · 对应代码 `458df8b` 及之前的安全提交
> 形态 A 状态：**已全部落地** —— 转发头走私已修、Cloudflare Access 已接入并实测、
> 外链 token 通道已退役（Access-only）。

---

## 0. 一页结论

| | 形态 A：有域名（named） | 形态 B：无域名 |
|---|---|---|
| **现状** | `304456.xyz` + named tunnel | quick 隧道随机域名 / 不启隧道 |
| **域名可枚举性** | 高（证书进 CT 日志，可被公开搜到） | 低（随机串，无人知道） |
| **主鉴权手段** | **Cloudflare Access**（按人授权、可撤销） | 外链 `?token=`（单因子） |
| **token 是否还要** | **已关掉**（2026-10-02 浏览器实测通过后置 `token_fallback: false`，见 A6） | 必须留（唯一凭据） |
| **整改状态** | **已完成（2026-10-02）**：代码侧 + CF 配置 + 浏览器登录全链路实测通过，外链 token 通道退役 | 已达标，只需守住分发纪律 |
| **最大残留风险** | 同局域网免鉴权直达（§7-R1，未修）；飞书深链需在 webview 内完成 Access 登录 | 链接转发即等于授权，无法按人撤销 |

**一句话**：有无域名决定的不是「要不要鉴权」，而是**用哪种鉴权**。有域名把「隐蔽性」丢掉了，所以必须换成可撤销的强身份；无域名靠隐蔽性还能撑，但要把 token 当密码管。

---

## 1. 资产与攻击面

### 1.1 拿到这个面板 = 拿到什么

这个问题决定了整改的下限。Pieqi 不是普通后台：

- 能在**本机**驱动 agent 执行命令、读写文件、改主仓库
- 能审批 agent 的高危工具调用（`auto_approve_tools` 默认放行 edit/delete/move）
- 能管控隧道本身（开关、续期）
- 能重新绑定飞书管理员账号
- 任务事件流里含 agent 将要执行的完整命令

**结论：一旦被未授权访问，等价于本机任意代码执行。**所以鉴权只能加强，不能取消。

### 1.2 三条入口

| 入口 | 对端形态 | 鉴权闸门 |
|---|---|---|
| 本机 / 内网直连 | `127.0.0.1` 或 RFC1918 | `ExternalAuthMiddleware` 判内网 → **全放行** |
| 隧道（quick 或 named） | 回环（cloudflared 子进程回源） | 靠转发头区分内外 → 判为外部后走凭据通道 |
| 飞书 IM 命令 | 经 Bridge，非 HTTP | lark 渠道 + `adminBinding.Match` |

### 1.3 凭据清单

| 凭据 | 作用 | 存放 | 轮换时机 |
|---|---|---|---|
| `TUNNEL_TOKEN` | cloudflared 连 Cloudflare、建立 named 隧道 | 环境变量 > 配置 > 仓外文件 `~/.pieqi/cloudflared_token` | 手动重签 |
| 外链 `?token=` | 浏览器访问面板 | 内存态（`TokenStore`） | 每次 `Start` / `RenewToken` / 隧道自愈 |
| Cloudflare Access JWT | 外网主身份 | CF 侧签发，回源头注入 | CF 会话过期 |
| `DebugSwitch` | 全局跳过所有鉴权 | 进程内 | 仅开发环境置 true |
| 飞书绑定 | 谁可发 IM 命令 | `~/.pieqi/feishu_binding.json` | 重新 bind |

---

## 2. 威胁模型

按**可远程利用性**排序，前三项是真正要命的：

### T1 · 转发头走私 → 鉴权完全绕过（**已修**，2026-10-02）

旧 `ClientIP` 无条件取 `X-Forwarded-For` **首跳**，而「内网判定」建立在它之上。到达回环端口的隧道流量默认就是内网，唯一能把它判成外部的只有转发头 —— 而首跳恰好是攻击者可控的那一段。

实测证据（修复前）：`GET /api/tasks` 无 token → 401；加一个 `X-Forwarded-For: 127.0.0.1` → **200**。伪造头连「仅内网」的绑定闸门都能穿过。

→ 修法见 §4-A1。

### T2 · 域名可枚举（**有域名特有**）

`304456.xyz` 的证书是 Let's Encrypt 签发，**必然会写进证书透明日志（CT）**，任何人都能用 crt.sh 之类枚举出来。以前 trycloudflare 的随机域名自带一层遮蔽 —— 换成固定域名后，这层遮蔽**归零**。

→ 这是「有了域名反而更需要强鉴权」的根本原因，见 §3。

### T3 · 局域网无凭据直达（**未修，需决策**）

服务监听 `0.0.0.0:3000`（代码 `r.Run(fmt.Sprintf(":%d", port))`），而 `IsInternalIP` 把 `192.168/16`、`10/8`、`172.16/12` 全部算作内网 → **内网判定直接放行**。

实测：本机 WLAN 地址 `192.168.1.61`，`curl http://192.168.1.61:3000/api/tasks` → **200，无任何凭据**。

这意味着同一 WiFi / 同一办公网内的任何设备（含访客手机）都能全量访问。→ 修法见 §7-R1。

### T4 · token 从 URL 泄漏

`?token=` 出现在地址栏、浏览器历史、Referer、截图、聊天记录里。转发链接 = 转发完整权限，且**无法单独撤销**（要撤销只能轮换，那所有人一起掉线）。

### T5 · token 暴力破解

32 位 base32 ≈ 160 bit，暴力破解不可行；但接口本身会被刷。→ 已有 `IPLimiter`（默认 5 次/分 → 拉黑 10 分钟）。

### T6 · Debug 开关误开

`auth.debug_skip_all_auth: true` 时全部鉴权短路。默认 false，但**没有任何运行时防护**（防的是"线上被误开"）。

### T7 · 孤儿 cloudflared 进程

旧实例被强杀后，其 cloudflared 子进程会继续跑（`cleanupOrphans` 只在 `Start` 里调用，不在 boot）。后果：域名"看起来还活着"，而新实例报 `active:false`，状态与事实不一致，运维判断被误导。已实测遇到（PID 28216，父进程已死）。

### T8 · 隧道凭据泄漏面

token 若走命令行参数，`tasklist` / `wmic` / `/proc/<pid>/cmdline` 都能读到。→ 已改为环境变量传递 + 仓外文件 + 日志脱敏。

---

## 3. 两种形态的本质差异

```
形态 A：有域名（named tunnel）
  公网 → Cloudflare 边缘 → [Access 策略] → 隧道 → cloudflared(本机) → 127.0.0.1:3000
                  ↑ 可挂 SSO / 邮箱 OTP，按人授权，随时撤销
                  ↑ 域名固定 → 证书进 CT → 人人可枚举

形态 B：无域名（quick tunnel）
  公网 → Cloudflare 边缘 → 隧道 → cloudflared(本机) → 127.0.0.1:3000
                  ↑ 无策略可挂：hostname 是随机串且会被回收
                  ↑ 域名随机 → 无人知道 → 唯一的"防护"是不可预测性
```

| 维度 | 有形 A（named） | 无域名 B（quick） |
|---|---|---|
| 域名稳定性 | 永久不变 | 会被 CF 周期回收（DNS 变 NXDOMAIN） |
| 可否挂 CF Access | **可以**（Access 需固定 hostname） | **不可以** |
| 可否注册为飞书 H5 可信域 | 可以 | 不可以 |
| 证书 | 自有域名，自动签发 | CF 代签 |
| 链接是否稳定 | 稳定 | 回收后失效，需重新分发 |
| 自愈判死信号 | **502 + 兜底页 `error code: 502`** | **530**（`Cloudflare Tunnel error`） |
| 鉴权正确形态 | Access 主 + token 兜底 → 最终 Access-only | token 单因子 |
| "隐蔽性"作为防护 | **不成立**（CT 可枚举） | 成立，但脆弱（一次转发即失效） |

**两条关键推论：**

1. **无域名不能上 Access，有域名才谈得上"换成身份鉴权"。** 所以有域名后的问题不是"还要不要 token"，而是"能不能换更好的"。
2. **不能靠"域名没人知道"当防护。** 这是隐蔽性，不是安全边界 —— 对形态 B 也只在"没人转发"时成立。

---

## 4. 形态 A：有域名（named）整改清单

### A1 · 修复转发头走私 ✅ 已完成（`25aea94`）

`ClientIP` 改为「**转发头只在立即对端可信时才采信**」：

1. 对端不是回环 → **对端地址即唯一事实，转发头一律不看**（这是修复核心）
2. 对端是回环 → 优先 `CF-Connecting-IP`（CF 边缘覆盖写入，客户端预置会被 CF 直接 403）
3. 退化到 `X-Forwarded-For` 的**末段**（CF 把真实客户端 IP 追加在末尾）
4. 都取不到 → 回环对端

> **不变量**：`cmd/pieqi/main.go` 的 `LocalURL` 恒为 `http://localhost:<port>`，即 cloudflared 必从回环回源。改那里必须同步复核 `isTrustedForwarder`，否则隧道流量会被误判成内网。

**验证结果**（线上实测）：

| 请求 | 修复前 | 修复后 | 判定来源 |
|---|---|---|---|
| 无伪造头 | 401 | 401 | `ip=真实公网IPv6, src=cf` |
| `XFF: 127.0.0.1` | **200** | **401** | `src=cf` |
| `XFF: 10.0.0.1, 1.2.3.4` | **200** | **401** | `src=cf` |
| 伪造入 bind 闸门 | 400 | 403 | `external_blocked` |

第三条是关键：客户端预置的 `1.2.3.4` 没被采纳，源站取到真实出口 `116.22.3.237` —— 证明被采信的那一跳由 CF 追加、**客户端不可控**。

### A2 · 接入 Cloudflare Access ✅ 代码侧已完成（`458df8b`）

`internal/auth/access.go` 做**源站侧验签**（只用标准库，未引第三方 JWT 依赖）：

- 取自 `Cf-Access-Jwt-Assertion` 头（缺失时退到 `CF_Authorization` cookie）
- 用团队域 JWKS 验 **RS256** 签名
- **强制 `alg=RS256`** —— 显式拒绝 `none` / `HS256`（防算法混淆）
- 核 `aud` 绑定本应用（防同团队其他应用的 token 被复用）
- 核 `iss` 绑定团队域、核 `exp`/`nbf`（60s 时钟偏移容忍）
- **`exp` 缺失即拒**（签名有效但无过期 = 永久凭据）
- JWKS 缓存 1h，未知 `kid` 重拉带 30s 最小间隔（防被当拉取放大器）
- 错误信息只说明失败类别，**绝不含 token 本体**

中间件顺序：`debug → 内网 → 限流黑名单 → Access → token 兜底`。两道全关 → 外网一律 401（deny-by-default），启动时另打 WARN。

> ⚠️ **光在 CF 控制台挂 Access 是没用的。** 源站不验签的话，伪造一个同名头就绕过了 —— 那 Access 只是装饰。这条已写进 `config.example.yaml` 注释。

### A3 · CF 控制台配置 ✅ 已完成（2026-10-02）

**团队域不必手抄**：Access 未登录时边缘会 302 到
`https://<团队>.cloudflareaccess.com/cdn-cgi/access/login/<hostname>?...`，
`Location` 头里同时带着**团队域和 AUD tag**，一条命令就能读出来：

```bash
curl -s --noproxy '*' -D - -o /dev/null https://304456.xyz/ | grep -i '^location:'
```

本机实测拿到的值（已写入 `config.yaml`）：

| 项 | 值 |
|---|---|
| 团队域 | `https://crimson-cell-a9cc.cloudflareaccess.com` |
| AUD | `f499e287…bfd1a`（64 位；完整值仅在 `config.yaml`，该文件已 gitignore） |
| JWKS | 团队域默认 `/cdn-cgi/access/certs`（实测 200，2 把 2048 位 RS256 公钥） |

> 团队域与 AUD 均可由上面的 `curl` 未认证读出，故团队域在本文档保留完整；
> 但 AUD 依旧按"凭据类信息不入库"的纪律在文档里截断 —— 本文档进的是
> **公开仓库**。AUD 单独泄露无法伪造 JWT（需 CF 私钥），纪律意义大于风险意义。

> 边缘 meta JWT 用的 kid `10f9ce40…` **也在**团队 JWKS 里 —— kid 查找能命中，
> 不会出现 `unknown kid`。
>
> 三方交叉验证 AUD 没抄错：登录 URL 的 `kid=` 查询参数、meta JWT 的 `aud` 声明、
> 以及你从控制台复制的值 —— **三者完全一致**。meta JWT 的 `alg` 实测为 `RS256`
> （与校验器强制项一致）。meta token 是 `type: meta` 的登录页凭据、**不带 `iss`**，
> 属正常；真正的会话 JWT 的 `iss` 才是团队域，校验器比对时已做大小写与尾斜杠归一
> （`TrimSuffix(iss,"/")` vs `TrimSuffix(team,"/")`，`EqualFold`），故配置里带不带
> 尾斜杠都安全。

**为什么不走环境变量**：AUD 原先是建议用 `PIEQI_AUTH_CLOUDFLARE_ACCESS_*` 的，
理由是当时 `config.yaml` 还在 git 里。现在它已被 `.gitignore` 排除
（`.gitignore:90`），写进去不会入库。反而用环境变量有风险 —— `start.bat` /
`restart.bat` 都不导出环境变量，一旦 Access 配置随重启丢失、又叠加
`token_fallback: false`，就会把所有人锁在门外。故最终落 `config.yaml`。

### A4 · Access 生效的实测验证 ✅

| 探针 | 结果 | 说明 |
|---|---|---|
| 启动日志 | `cloudflare access enabled {team_domain: …, token_fallback: true}` | 校验器构造成功 |
| 外网未登录 `GET /api/tasks` | **302** → `crimson-cell-a9cc.cloudflareaccess.com/cdn-cgi/access/login/…` | 边缘拦截生效 |
| 同上，源站审计记录 | **0 条** | 请求**根本没到源站**，边缘是第一道闸 |
| 内网 `http://127.0.0.1:3000/api/tasks` | **200** | 合法路径未被改坏 |
| `internal/auth` + `internal/config` 测试 | 全绿 | 验签正反两面 36 个用例 |

> **这一步已由人工实测通过（2026-10-02 14:28）** —— 源站审计里出现了不带任何 `?token=`
> 却放行的记录，这是全链路成立的**决定性证据**（`curl` 拿不到有效 JWT，只有真登录才会有）：
>
> ```
> 14:28:57  op=biz.api  ip=2409:8a55:...  src=cf  method=access  email=edr***@gmail.com
> 14:29:05  op=biz.api  ip=2409:8a55:...  src=cf  method=access  email=edr***@gmail.com
> 14:32:17  op=biz.api  ip=2409:8a55:...  src=cf  method=access  email=edr***@gmail.com
> ```
>
> （邮箱按隐私脱敏，日志原文为完整地址。）
>
> 共 10 条 `auth_method=access`。注意 `src=cf` —— 来源 IP 由 `CF-Connecting-IP` 判定，
> 且 `email` 是 Access 策略放行的那个身份。**"边缘 302" 只证明 Access 策略生效，
> 这条才证明源站的 JWT 验签也真的过了。**

### A5 · ⚠️ Access 的连带影响：预览外链会失效

Access 按 **hostname** 生效、不区分路径 —— 实测 `/preview/*` 同样被 302 到登录页。

| 链路 | 之前 | Access 开启后 |
|---|---|---|
| 飞书深链（IM 命令推送的链接） | 带 `?token=`，webview 直接打开 | 需在**飞书 webview 内完成 Access 登录**，OTP 流程多半走不通 |
| 外链预览（`/preview/<taskID>/` 分享） | 带 `?token=`，任何人可看 | 对方必须能过 Access 策略 → **分享给团队外的人失效** |

> 隧道**启动**本身不受影响 —— 飞书 IM 命令走后端直接调用，不经 HTTP。

三种处理，**已选定并执行 ①（2026-10-02）**：

- **① 全量保护 ✅ 已选并已生效（2026-10-02）** —— 接受预览只给能过 Access 的人看。
  `/preview/*` 不单开策略，与全站同一等级。代价：**把预览分享给没有 Access 账号的人会失效**。
  直接后果：**token 通道退役**（见 A6，已执行）。
- **② 预览路径放行（未选，留作日后备选）** —— 若将来确实需要把预览分享给外部的人，
  给 `/preview/*` 另建一个 Access 应用、策略设 **Bypass**，预览退回 `?token=` 保护。
  **无需改代码**：源站收不到 JWT → `Access.Verify` 失败 → 自动落到 token 通道
  （这正是 `ExternalAuthMiddleware` 的设计顺序）。**届时必须把 `token_fallback` 改回
  `true`**，否则那条路径无人可进。
- **③ 暂不动** —— 不选。

> 为什么 ① 的代价是可以接受的：这个面板能在本机执行 agent 命令、读写文件，
> 预览外链的价值远低于它被陌生人拿到的风险。要给别人看产出，更稳的做法是把产物
> 单独导出后走别的渠道发（不经过面板），而不是把面板的一角对外开放。

另有一条**替代路径**，不影响本次决策：Cloudflare Access 的 **Service Token**
（请求头 `CF-Access-Client-Id` / `CF-Access-Client-Secret`）能给机器调用发凭据，
适合日后"要自动化访问面板 API"的场景。它是**长期不变**的凭据，属主动降级，
别为图一时方便就开；真要用，配一条独立策略并单独记到期时间。

### A6 · 关掉 token 兜底，走 Access-only ✅ **已执行（2026-10-02 14:34）**

```yaml
auth:
  cloudflare_access:
    token_fallback: false    # → TokenDisabled，URL 不再需要 ?token=
```

收益：URL 干净、书签永久有效（不再随重启轮换）、授权按人管、可单独撤销。

**为什么先 `true`、确认后才改 `false`（已按此顺序执行）**：

1. **Access 在边缘拦截，token 兜底从外部根本走不到**。实测：外网请求（含 `/api/tasks`、
   `/preview/*`）一律 **302** 到登录页，源站审计记录 **0 条**。所以 `token_fallback: true`
   在验证期**没有任何可达的暴露面**，留着是保险、不是风险。
2. **Access 那一段无法用 curl 自证**（未登录只拿 302）。在验证不了的状态上关掉唯一
   后备通道，万一失败就是"外网整体 401"。故必须先有浏览器实登录通过的证据（见 A4）。

**执行时的前置检查（都已过）**：

- 已确认**没有任何外部服务会主动 POST 进源站**（否则 Access 会把回调一起拦掉）：
  飞书渠道是 `channels.lark.event_mode: longconn`（Pieqi 主动 wss 出去，不需公网入站）；
  全量路由清点后，唯一的非鉴权入站是 `POST /internal/hook`，那是 hook 子进程回连、
  仅本地。→ 关 token 不会打断任何自动化。
- `tokenDisabled = Access.Enabled && !TokenFallback`（`cmd/pieqi/main.go`）—— "两道凭据
  通道都关"的启动 WARN 只在 **Access 构造失败**时触发，故 Access-only 不会误报。

**执行后的实测**（重启加载配置）：

| 探针 | 结果 |
|---|---|
| 启动日志 | `cloudflare access enabled {team_domain: …, token_fallback: false}` |
| 启动 WARN「无任何可用凭据通道」 | **未出现**（正确：Access 是活通道） |
| 内网 `127.0.0.1:3000/api/tasks` | **200**（本机/内网不受影响） |
| 外网 `https://304456.xyz/api/tasks` | **302** → Access 登录页 |
| 源站：伪装外网对端 + 无 JWT（`CF-Connecting-IP: 1.2.3.4`） | **401** |
| 源站：同上 + 乱造 `?token=` | **401**（审计 `has_token_query: true` 仍拒 → 通道确已关） |
| 源站：`/preview/test/` 同上 | **401**（与 ① 决策一致） |
| `internal/auth` + `internal/config` 测试 | 全绿 |

> 为什么"乱造 token 被拒"能证明通道关了：同一请求在 `token_fallback: true` 时会走
> `TokenStore.Validate`（无效 token 同样 401，**不可区分**）；能区分的是**结构性**证据 ——
> `middleware.go` 的 `if !s.TokenDisabled` 分支，token 通道在配置层被整体跳过。

**旧书签不会失效**：带 `?token=…` 的老 URL 仍可用 —— 边缘先做 Access 认证并注入 JWT，
源站按 Access 放行，`?token=` 被忽略。区别只是它**不再是凭据**。

**回退**：把 `token_fallback` 改回 `true` 并重启即可（只读配置，无需重新编译）。

### A7 · 有域名形态的目标态

```
公网 → CF 边缘 [Access: 邮箱 OTP / 指定白名单]
     → 隧道 → 本机
     → 内网判定（转发头纪律）→ 外部
     → Access JWT 验签通过 → 放行，审计记录 auth_method=access + 邮箱
     （A5-① 已执行：token 通道已关，URL 无凭据 — 即 §A6 的当前态）
```

---

## 5. 形态 B：无域名整改清单

### B1 · quick 隧道（随机域名）

**前提认知**：quick 域名是随机串且会被 CF 回收，因此

- **不能挂 Access**（Access 需要固定 hostname）
- **不能注册为飞书 OAuth / H5 可信域**
- token 单因子是**当前唯一可行**的方案，不算将就

所以这个形态要做的不是"换鉴权"，而是**把 token 当密码管**：

| 动作 | 说明 |
|---|---|
| **不要转发链接** | 链接 = 完整权限，且无法按人撤销 |
| **用短 TTL** | `auth.cloudflared.default_ttl`，默认 `15m`；可选 `15m/1h/4h` |
| **限定分发渠道** | 只经飞书私聊，不走群、不截图 |
| **保持限流开启** | `ratelimit.max_failures_per_min: 5` / `blacklist_duration: 10m` |
| **依赖自愈** | 域名被回收 → 530 判死 → 自动换新域名+新 token → 重新分发 |
| **不开 debug** | `debug_skip_all_auth` 必须为 false |

**取舍要清楚**：无域名形态的安全性**完全建立在"没人知道域名"上**。一旦有人拿到链接（转发、日志、截图），防护即失效且无法追溯。这是它相对有域名形态的**结构性弱点**，不是配置能补的。

### B2 · 纯内网，不启隧道（最安全）

不暴露公网 = 无形态 A/B 的所有公网威胁。此时唯一要守的是：

- **不要误开隧道**：确认 `GET /api/tunnel/status` 为 `active:false`，且无残留 cloudflared 进程
- **仍然要修 T3**（§7-R1）：`0.0.0.0` 监听 + RFC1918 算内网 = 同网段任意设备全量访问。**"不暴露公网"不等于"安全"**
- **别开 debug**

---

## 6. 配置开关矩阵

| 配置项 | 无域名（quick） | 有域名（named）· 初配 | 有域名 · 目标态 |
|---|---|---|---|
| `auth.cloudflared.mode` | `quick` | `named` | `named` |
| `auth.cloudflared.public_hostname` | — | `304456.xyz` | `304456.xyz` |
| `auth.cloudflared.tunnel_token` | — | 留空（走仓外文件/env） | 同左 |
| `auth.cloudflared.default_ttl` | `15m` | `15m` | 不再重要（URL 不带 token） |
| `auth.cloudflare_access.enabled` | `false`（不适用） | **`true`**（✅ 已配） | `true` |
| `auth.cloudflare_access.team_domain` | — | `crimson-cell-a9cc.cloudflareaccess.com` | 同左 |
| `auth.cloudflare_access.audience` | — | AUD tag（落 `config.yaml`，已 gitignore） | 同左 |
| `auth.cloudflare_access.token_fallback` | 不适用 | ~~`true`~~ → **`false`**（2026-10-02 已切换） | ✅ **`false`**（Access-only，当前态） |
| `auth.ratelimit.max_failures_per_min` | `5` | `5` | `5` |
| `auth.debug_skip_all_auth` | `false` | `false` | `false` |
| `server.port` 监听范围 | 建议 `127.0.0.1` | 建议 `127.0.0.1` | 建议 `127.0.0.1` |

---

## 7. 残留风险与待办

### R1 · 局域网无凭据直达 ⚠️ **未修（建议优先）**

**现象**：`r.Run(":3000")` 监听 `0.0.0.0`；`IsInternalIP` 认 `192.168/16`、`10/8`、`172.16/12` 为内网 → 同网段任意设备免鉴权全量访问。已实测 `http://192.168.1.61:3000/api/tasks` → 200。

**修法（二选一）**：

- **方案 1（推荐）**：监听地址改为 `127.0.0.1`。cloudflared 走 `http://localhost:<port>`，本机进程也走回环，功能不受影响；局域网设备直接连不上。可加 `server.bind` 配置项（默认 `127.0.0.1`，需要 LAN 访问时显式改）。
- **方案 2**：保留 `0.0.0.0`，但把"内网"从 RFC1918 收窄为**仅回环**。代价：真·局域网内的合法使用（如手机连同一 WiFi 直连面板）会被拒，需要走隧道。

### R2 · `IsInternalIP` 与 `isTrustedForwarder` 的语义不一致

`isTrustedForwarder` 只认**回环**（正确）；`IsInternalIP` 认**整个 RFC1918**。两者混用会导致"可信转发器"与"内网放行"边界不同。收窄后建议统一为回环语义。

### R3 · Debug 开关无运行时防护

`debug_skip_all_auth: true` 会静默全放行。建议加两道保险：(1) 启动时若为 true 打 **ERROR 级**日志；(2) 外网请求命中 debug 放行时也记审计（目前 `auth_method=debug` 已记录，但外网场景应额外告警）。

### R4 · 孤儿 cloudflared 进程

`cleanupOrphans` 只在 `Start` 里调用、不在 boot。建议开机/启动时也清一次，避免"域名还活着但实例说 inactive"的状态错乱。

### R5 · 未修的安全项（历史遗留）

- ~~`ClientIP` 无条件信任 XFF 首跳~~ → **已修**（§4-A1）
- Universal SSL 签发延迟：新域名首次接入时 HTTPS 可能报 TLS alert 40，此时 HTTP 正常。属 CF 侧时序，非漏洞，但会误导排查。

---

## 8. 验证方法（自证整改生效）

> ⚠️ **Access 开启前后，外网探针的期望值不同。** Access 在**边缘**就把未登录请求
> 302 掉了，请求到不了源站 —— 所以「401/403」只在 Access 关闭时才看得到。
> 这也是为什么源站侧的转发头修复必须靠**单测**锁定，不能只靠外网 curl。

**A. Access 关闭时（验证源站闸门本身）**

```bash
# ① 外网无凭据 → 应 401
curl -s -o /dev/null -w "%{http_code}\n" https://304456.xyz/api/tasks

# ② 外网伪造转发头 → 应 401（这就是修复前的洞，修复前是 200）
curl -s -o /dev/null -w "%{http_code}\n" \
  -H "X-Forwarded-For: 127.0.0.1" https://304456.xyz/api/tasks

# ③ 伪造内网入绑定闸门 → 应 403
curl -s -o /dev/null -w "%{http_code}\n" -X POST \
  -H "Content-Type: application/json" \
  -H "X-Forwarded-For: 192.168.1.1" -d '{}' https://304456.xyz/api/auth/bind
```

**B. Access 开启后（验证边缘闸门）**

```bash
# ④ 未登录 → 应 302 到 <团队>.cloudflareaccess.com（且源站零审计记录）
curl -s --noproxy '*' -D - -o /dev/null https://304456.xyz/api/tasks | grep -i '^location:'
```

**C. 与 Access 无关的两条基线**

```bash
# ⑤ 内网直连 → 应 200（合法访问未被改坏）
curl -s --noproxy '*' -o /dev/null -w "%{http_code}\n" http://127.0.0.1:3000/api/tasks

# ⑥ 源站侧的转发头修复 → 单测（走真实 gin 中间件链注入公网对端，curl 造不出来）
go test ./internal/auth/ -run 'TestTunnelPath|TestClientIP|TestIsInternalIP' -v
```

**D. 只能人工的一项**：浏览器用**不带 `?token=`** 的 `https://304456.xyz/` 打开，
走完 Access 登录能进面板 → `auth_method=access` 全链路成立（在审计日志里核对）。

审计日志（`~/.pieqi/logs/pieqi-YYYY-MM-DD.log`）里核对两个字段：

- `ip_source`：`cf` / `xff` / `peer` / `none` —— **这次凭什么判内网**
- `auth_method`：`debug` / `internal` / `access` / `token` / `external` / `none` —— **这次凭什么放行**

这两个字段是整改可回溯的基础。看到 `ip_source` 不是 `cf` 的外网请求，就该警觉。

---

## 附录 · 相关提交

| 提交 | 内容 |
|---|---|
| `574f92a` | 隧道凭据加固（移出 git / 环境变量传递 / 日志脱敏）+ 固定域名可配置 + 模板守卫 |
| `4e76038` | qoder spawn 命令解析加安装落点回退（与安全无关，同批处理） |
| `25aea94` | **修复转发头走私导致的鉴权完全绕过（CRITICAL）** |
| `458df8b` | **接入 Cloudflare Access 作为外网主鉴权，token 降级为兜底** |

配套测试：`internal/auth/forwarded_ip_test.go`、`internal/auth/access_test.go`、`internal/config/config_test.go`（`TestConfig_AccessDefaults` / `TestConfig_AccessEnvOverride` / `TestConfig_ExampleAccessSectionIsSafe`）。
