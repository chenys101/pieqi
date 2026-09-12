# 概览：2.2 版本 —— 需求研发流水线（S0–S5）

> 更新：2026-09-12 ｜ **S0–S1 = v8（G0 ✅ 已放行）** ｜ **S2 = v3（PRD，50 条 AC，G1 ✅）** ｜ **S3 = v3（技术方案，G2 ✅）** ｜ **S4 = v1（排期拆解）** ｜ **S5 = v5（开发实施；T1–T9 全 ✅）**

## 交付物

- `docs/requirements/S0-S1-2.2版本需求规划.md` — 需求规划（缺口盘点 + 需求池 + 范围 + 风险 + 决策 D1–D12 + G0 自查）
- `docs/requirements/S2-2.2版本PRD.md` — **PRD（EARS 句式 + 50 条可判定 AC，需求冻结基线）**
- `docs/requirements/S3-2.2版本技术方案.md` — **技术方案（接口契约 + 数据模型 + 边界规则 + WBS 17 项 ≤2 人日）**
- `docs/requirements/S4-2.2版本排期拆解.md` — **排期拆解（17 项 / 8 波次 / 里程碑 / 缓冲 / 跨批次冲突面）**
- `docs/requirements/S5-2.2版本开发实施记录.md` — **开发实施记录（T1–T9 全部落地；R3/R6/R8 的 2.2.0 范围闭环）**

## 阶段状态

| 阶段 | 产物 | 门禁 | 状态 |
|---|---|---|---|
| **S0–S1** 需求收集与分析 | v8 | **G0** | ✅ 已放行 2026-09-12 |
| **S2** 需求定义 | v3（50 条 AC） | **G1** | ✅ 已签署 2026-09-12（需求冻结生效） |
| **S3** 技术方案设计 | v3 | **G2** | ✅ 已签署放行 2026-09-12 |
| **S4** 排期拆解 | v1 | — | ✅ 已产出 |
| **S5** 开发 | **v5（T1–T9 完成）** | G3 | 🔄 **首批 17 项完成**；余 2.2.1 批次（R1/R2/R5） |

**下游链路**：PRD 的 50 条 AC = S6 用例 / S7 UAT 的**唯一验收口径**；S3 的接口与字段冻结后，改动走变更（带影响评估）。

---

## 本轮（W0 基线 + W1 组件库 + T2 输入条）交付了什么

### 1. W0 基线（M1 ✅）

| 靶 | 检查 | 结果 |
|---|---|---|
| 原型 | `_validate.mjs` / `_verify.mjs` / `_measure.mjs` | ✅ 全绿（0 错误 / 200 项通过 / 截断 0-5） |
| web | `vitest run` / `vue-tsc --noEmit` | ✅ 9 spec·65 tests / exit 0 |

### 2. W1 组件库（T1）→ M2 ✅ 组件接口冻结

| 新增 | 说明 |
|---|---|
| `components/ui/Input.vue` | 单行输入；`md`=h36 / `sm`=h32 |
| `components/ui/Textarea.vue` | 多行；`rows` 固定高 或 `autoResize` 自适应（封顶） |
| `components/ui/Select.vue` | `<select>` + `appearance-none` + 自绘 chevron |
| `components/ui/form-controls.spec.ts` | **14 条单测** |

| 改动 | 说明 |
|---|---|
| `styles/tokens.css` | **补 `--radius-*`**（SPEC §3.4 定义过、落地时从未写入 token 层） |

**统一外壳规格**（三件共用 → 也是后续 21 处迁移的基准）：`bg-surface-subtle` 底 ｜ `rounded-[var(--radius-sm)]`(6px) ｜ `focus-visible:ring-[3px]`+`ring-accent/40` ｜ `placeholder:text-text-tertiary`(4.8:1) ｜ `hover:border-border-strong` ｜ error 态 `border-error`+`aria-invalid`。

**验证**：`vitest` **10 spec / 79 tests 通过**（新增 14）｜ `typecheck` exit 0 ｜ `vite build` ✅ 6.00s，产物含 `.rounded-[var(--radius-sm)]` / `ring-[3px]` 等类。

> ⚠️ **关键洞察：R8 的病因是「web 相对原型漂移」，不是「SPEC 没定义」。** 原型 `.composer-box`/`.dlg-input` 早已用对 `--surface-subtle` 与 `--text-tertiary`；web 落地时写成 `bg-background`（= canvas）与 `text-muted/60`（≈3:1）。→ **T2 的正确参照物是原型实现，不是拿 SPEC 文字重新发明。**

### 3. W2 主体（T2）：输入条改造 ✅

| 改动 | 说明 |
|---|---|
| `Textarea.vue` | +`resizable`（输入条传 false）；**`defineExpose({ el, focus })`** —— 斜杠补全必需，否则上层只能再写裸 textarea（AC-R8-11 会失败） |
| `PromptInput.vue` | 裸 `<textarea>` → T1 `Textarea`（外壳一次收敛，顺带满足 AC-R8-11） |
| `InterveneInput.vue` / `NewTaskPage.vue` | 外条满宽 + 内层 `mx-auto max-w-3xl` 与正文同线；按钮 6px 圆角 + 伪元素命中区 ≥44 |
| `composer.spec.ts` | **+14 条单测**（外壳 / 命中区 / 斜杠补全 / 双态按钮） |
| `web/scripts/_measure-composer.mjs` | **web 靶点第一个三档实测脚本** |

**验证**：`vitest` **11 spec / 93 tests**（+14）｜ `typecheck` exit 0 ｜ `build` 5.30s ｜ **实测对齐 6/6（Δ=0）**、命中区 44/46、控制台零噪声 ｜ `go build` ✅ 且 `pieqi.new.exe` 已顶上 ：3000。

> ⭐ **判据：对齐错位只发生在 >768** —— ≤768 时 `max-w-3xl`(768) 不生效，两者本就同线；>768 正文块居中而输入条满宽（旧代码 1280 档错 **89px / 112px**）。**窄档实测测不出宽档的病**，与「靶点错配」同类。
> 另：顺带修掉 `detectQuery` 的「慢一拍」（读 `props.modelValue` 去配当下 `selectionStart` = 光标与文本不同源 → 补全识别慢一个字符），有单测守住。

### 4. 本轮发现的方案层问题（2 项）

| # | 问题 | 影响 | 处置 |
|---|---|---|---|
| **1** | ⚠️ **回归脚本靶点错配** —— `_validate/_verify/_measure` 读的是**原型 `index.html`**，不是 `web/` 产物；而 T1/T2/T3 改的是 `web/src/` | S4 把「跑这三个脚本」写进 **T4 出口条件** → 对 web 改动**零覆盖**，会「验完等于没验」 | web 侧改用 `vitest` + `build`/`typecheck`；视口实测需新脚本（靶点改 `web/dist`）—— **待确认** |
| **2** | **圆角命名错位** —— SPEC `--radius-sm/md/lg/xl`(6/8/12/16) 与 Tailwind 默认名**错位一格**（`rounded-md`=6px）；全站 ~109 处用默认名 | 若改 config 覆盖，全站圆角**集体漂移** | 本轮**不改 config**，组件显式写 `rounded-[var(--radius-sm)]` 锚 token；**统一方案列为 T3 的开工前置** |

### 5. 原型与 SPEC 的三处出入（历轮取值，AC 为准）

| 项 | 原型 | SPEC/PRD | 取值 |
|---|---|---|---|
| 输入类焦点 ring | 2px | **3px**（§7，AC-R8-03） | 取 **3px** |
| Select 实现 | 原生 + 自绘外观 | §4.4 提到自定义弹层 | **保留原生 `<select>`**（自研 combobox 风险 > 收益；R8 病因是样式不一致） |
| composer 结构 | textarea+按钮装进同一个 12px 圆角盒 | **AC-R8-05 要求 6px** | 取 **PRD**，输入框与按钮分开（T2 新发现） |

---

## 需要你的事

| # | 事项 | 性质 |
|---|---|---|
| **决策** | **无** —— D1–D12 全部结清 | — |
| — | **实际投入人力** —— S4 已给 1–4 人换算表；**由谁排、投几人**是你的决策（按约定我不代排期） | **排期方提供** |
| — | ⚠️ **靶点错配的处置确认** —— T2/T4 的验证口径按 web 侧（`vitest`+`build`）走？ | **待确认** |
| — | 阶段收尾 4 件事（建事项 / 挂交付物 / 加关注人 / 入库） | **待下指令** |
| — | 新增/更新产出（S4 / S5 / 三份更新）是否**再上腾讯文档 + 项目资料库** | **待指令** |
| — | 腾讯文档（4 份）✅ 已建 ｜ 项目资料库归档（4 份）✅ 已完成 | — |

---

## 载体索引

| 产出 | 腾讯文档（撰写与协作） | 项目资料库（归档） |
|---|---|---|
| S0–S1 需求规划 | https://docs.qq.com/aio/DTmNLaGxMemJ6VHdV | `2.2版本-产品需求全流程（S0–S3）/` |
| S2 PRD | https://docs.qq.com/aio/DTndGQmt1d01BU0Rr | 同上 |
| S3 技术方案 | https://docs.qq.com/aio/DTm9iSFFFWkRDRXFy | 同上 |
| 本概览 | https://docs.qq.com/aio/DTm9QSU1kTFBrYWFM | 同上 |
| **S4 排期拆解** | ⬜ 待指令（本地已产出） | ⬜ 待指令 |
| **S5 开发实施记录** | ⬜ 待指令（本地已产出） | ⬜ 待指令 |

> ⚠️ **已知差异（1 处，仅样式）**：腾讯文档副本中，S3 的两处 API 路径 `/api/bots/:id/prompt` **未使用行内代码反引号** —— 腾讯文档 WAF 会拦截「以 `prompt` 结尾的行内代码段」。**文本内容完全一致**，仅失去等宽样式。本地文件保持规范写法。
> ⚠️ **载体版本提示**：上表腾讯文档 / 资料库中的 S0–S1 与 S3 是**更新前的副本**（v7 / v2）。本地已升 v8 / v3。**是否同步覆盖以你的指令为准。**

---

## 流水线位置

```
S0–S1 ✅G0 ──▶ S2 ✅G1 ──▶ S3 ✅G2 ──▶ S4 ✅ ──▶ S5 开发/G3 🔄 进行中
                                                      │  M1/M2 ✅ · W2 已解锁
                                        S6 测试/G4 ─▶ S7 验收/G5 ─▶ S8 上线/G6 ─▶ S9 复盘/G7
```

S5 已完成 **T1（组件库）/ T2（输入条改造）**，下一任务是 **T3a / T3b（存量迁移，21 处清零裸控件）**；T4（R8 视觉回归）建议沿用 `_measure-composer.mjs` 的 web 靶点思路，不再依赖原型三脚本给 web 背书。
> ✅ **:3000 已由 `pieqi.new.exe`（37.7MB，含新前端）承载**。⚠️ 原 `pieqi.exe` 进程（及 cloudflared）在会话间被回收过一次，服务曾短暂中断 —— 后台起的进程不保险，开工前先探活 `:3000`。要看交互细节也可走 dev server（:5174）。
