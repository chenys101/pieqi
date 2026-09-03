# Pieqi「反馈体系」功能点规划

> Status: Draft  
> Scope: Feedback & Control Plane  
> Positioning: Agent 工作结果的观察、验证、回退与干预  
> Updated: 2026-09-03

---

## 1. 定位

Pieqi 的「反馈体系」不是简单的 Feedback Panel，而是一套围绕 Agent Task 的：

> **Observe → Verify → Control → Intervene**

系统。

核心目标：

```text
Agent 修改项目
      ↓
  我改了什么？
      ↓
   Changes
      ↓
  具体改了什么？
      ↓
    Diff
      ↓
  实际效果如何？
      ↓
 Preview / Checks
      ↓
   是否满意？
   ├── Approve
   ├── Rewind
   └── Continue / Intervene
      ↓
    Evidence
      ↓
    Task Outcome
```

核心原则：

- **先可见，再可跑，再可回，再闭环**
- Feedback 基于 Task / Timeline / Event，而不是独立维护一套事实
- 后端负责进程、工作区、状态与 Provider 真相
- 前端负责展示与交互
- Feedback 能力通过 Provider 扩展，而不是写死技术栈
- 手机优先满足「控制与验收」，PC 再提供更深的查看能力
- 不把 Pieqi 演变成 Web IDE / Computer Use 平台

---

# 2. 优先级定义

| 优先级 | 含义 |
|---|---|
| **P0** | 当前阶段必须实现，缺失会导致 Feedback 主链路不成立 |
| **P1** | 高价值能力，完成后形成完整 Control Plane |
| **P2** | 有明显价值，但可以后置 |
| **P3** | 探索性能力，暂不进入主 RoadMap |
| **❌** | 当前定位下不建议投入 |

---

# 3. P0：Feedback MVP

目标：

> Agent 改代码后，用户能够快速知道「改了什么」，查看 Diff，并能够验证、回退。

---

## 3.1 Feedback 入口

### 功能

Session / Task 内提供统一 Feedback 入口。

PC：

```text
Task
 ├── Timeline
 └── Feedback
```

Mobile：

```text
Task
 ├── Timeline
 └── Feedback Drawer
```

### 验收

- 任意 Task 可以进入 Feedback
- Feedback 不依赖特定 Agent
- Mobile / PC 均可访问

---

## 3.2 FileChange Domain Model

建立统一的文件变更模型。

```text
FileChange

id
taskId
turnId
path
operation
toolUseIds
status
timestamp
```

operation：

```text
create
modify
delete
rename
```

status：

```text
pending
success
failed
```

### 原则

FileChange 属于 Feedback Domain。

不要直接绑定 Git。

---

## 3.3 Tool Event → FileChange

从已有 Tool Event / Tool Result 中增量聚合：

```text
Edit
Write
Delete
Rename
```

形成：

```text
Turn
 ├── File A
 ├── File B
 └── File C
```

优先复用现有 Task / WS Event 数据，不新增复杂事件系统。

---

## 3.4 Turn Change List

Feedback 中按 Turn 展示：

```text
Turn #12

用户：
修改登录页面样式

3 files changed
+82 -31

src/Login.vue
src/style.css
src/components/Button.vue
```

### 验收

一次包含多个 Edit / Write 的 Task：

- 能按 Turn 分组
- 能看到文件列表
- 能看到基础变更统计
- 能定位到 Timeline

---

# 4. ⭐ P0：Change Summary

这是 Feedback MVP 的核心 UX。

目标：

> 不要求用户先看 Diff，就能知道 Agent 做了什么。

例如：

```text
本轮变更

3 files changed
+82 / -31

主要变化

• 修改登录页面布局
• 增加 Button loading 状态
• 调整移动端样式
```

第一阶段可以纯规则生成：

```text
文件数量
新增/删除数量
文件路径
operation
```

后续再增加 Agent Summary。

---

# 5. Timeline ↔ Feedback 联动

建立双向跳转：

```text
Timeline
   ↓
Turn
   ↓
Changes
```

以及：

```text
FileChange
   ↓
Tool Event
   ↓
Timeline
```

用户可以：

- 从 Turn 查看修改文件
- 从文件定位对应 Tool Event
- 从 Tool Event 查看对应 Diff

---

# 6. P0：Single File Diff

支持单文件 Diff。

第一阶段优先使用 Tool 入参中的：

```text
Edit old/new
Write content
```

实现：

```diff
- old
+ new
```

能力：

- unified diff
- additions / deletions
- 大文件截断
- 二进制文件跳过
- Lazy Load

---

# 7. P0：Task Baseline

定义：

> Task 开始之前，工作区是什么状态？

Baseline 支持：

```text
Git Commit
Snapshot
```

建议抽象：

```text
TaskBaseline

type:
  git
  snapshot

ref:
...
workspace:
...
createdAt:
...
```

不要假设所有 Task 都必须存在 Git。

---

# 8. P0：Checkpoint

为 Rewind 建立代码检查点。

建议：

```text
User Turn
    ↓
Checkpoint
    ↓
Agent 修改
    ↓
Turn Result
```

Checkpoint 至少覆盖：

- 被修改文件
- 文件内容
- workspace 信息

第一阶段不追求完整机器快照。

---

# 9. P0：Code Rewind

提供：

```text
POST /api/tasks/:id/rewind
```

概念参数：

```json
{
  "to_turn": 12,
  "scope": "code"
}
```

行为：

```text
Turn #12
   ↓
Checkpoint #12
   ↓
恢复代码
```

### 重要原则

**Timeline 不删除。**

Rewind 后：

```text
Timeline
────────────────
Turn 10
Turn 11
Turn 12
Turn 13 ← Rewind 前轨迹
Turn 14 ← Rewind 前轨迹

       ↓

Rewind Event
```

代码状态可以回退，但历史事实保持可审计。

---

# 10. P0：Preview Provider

## 定义

Preview 是：

> **项目运行态反馈能力。**

不是代码预览，也不是 Web IDE。

典型流程：

```text
Agent 修改 Vue
      ↓
Preview Provider
      ↓
npm run dev
      ↓
127.0.0.1:5173
      ↓
Preview Proxy
      ↓
手机
```

---

## 10.1 Preview 不是所有 Task 都需要

Preview 是可选 Feedback Capability。

状态：

```text
unavailable
available
recommended
running
```

例如：

### Go Bug Fix

```text
Preview: unavailable
```

### Vue 页面修改

```text
Preview: recommended
```

### 普通前端代码修改

```text
Preview: available
```

---

# 11. Preview 启动方式发现

Pieqi 不应该简单硬编码：

```text
npm run dev
```

而采用：

```text
Project Discovery
        +
Agent Intent
        ↓
Preview Decision
        ↓
Preview Provider
```

---

## 11.1 Project Discovery

第一阶段支持：

```text
package.json
vite.config.*
next.config.*
nuxt.config.*
```

发现：

```text
framework
command
port
cwd
```

例如：

```text
framework: vite
command: npm run dev
port: 5173
cwd: .
```

---

## 11.2 Project Preview Profile

允许项目保存：

```text
Preview Profile

framework: vite
command: npm run dev
port: 5173
cwd: frontend/
```

以后 Task 复用。

---

## 11.3 Agent Intent

未来允许 Agent 声明：

```json
{
  "preview": {
    "recommended": true,
    "reason": "UI changes"
  }
}
```

Pieqi 决定是否启动。

---

# 12. P0：Preview Lifecycle

Provider 负责：

```text
start
stop
status
restart
```

要求：

- 绑定 `127.0.0.1`
- 使用 Task 独立端口
- Task 结束自动清理
- 异常退出可感知
- 防止端口冲突

---

# 13. P0：Preview Proxy

Preview 本身不能直接暴露本地端口。

采用：

```text
Mobile
   ↓
Pieqi
   ↓
Authenticated Preview Proxy
   ↓
127.0.0.1:5173
```

要求：

- Task 鉴权
- Preview 鉴权
- 防止任意端口代理
- Tunnel 环境可访问
- 不直接暴露 localhost

---

# 14. P1：双视角 Changes

提供：

```text
本轮变化
```

以及：

```text
相对 Task Baseline 的累计变化
```

即：

```text
Event View

Turn #12
 ├── A
 └── B

Turn #13
 ├── B
 └── C
```

与：

```text
Baseline View

Task Start
   ↓
A modified
B modified
C created
```

---

# 15. P1：Approval → Diff

审批卡片直接进入 Diff：

```text
Agent 请求审批

将修改 3 个文件

[查看 Diff]
[批准]
[拒绝]
```

提升审批质量。

---

# 16. P1：Checks Provider

建立统一 Check Provider。

```text
Check Provider

├── Go
├── Node
├── Java
├── Python
└── Custom
```

统一结果：

```text
Check

name
status
command
duration
exitCode
output
```

第一阶段可以先复用 Agent 已产生的 test / lint / build Tool Result。

---

# 17. P1：Check Result

Feedback：

```text
Checks

✓ npm test
✓ npm lint
✗ npm build
```

支持：

- 状态
- Exit Code
- Duration
- Output
- Error 摘要

---

# 18. P1：Check Rerun

允许用户重新执行：

```text
[Run Check]
```

要求：

- 超时
- 日志流式输出
- Exit Code
- Approval / Permission 策略
- 运行状态

---

# 19. ⭐ P1：Task Outcome

Task 最终形成结构化结果。

例如：

```text
Task Outcome

状态：⚠️ 部分完成

Changes
8 files
+321 / -87

Preview
✓ Ready

Checks
✓ npm test
✓ npm lint
✗ npm build

Issues
• build failure

Actions

[继续处理]
[查看 Diff]
[回退]
```

它比完整 Timeline 更适合手机端验收。

---

# 20. ⭐ P1：Rewind → Verify

Rewind 不应该只是恢复文件。

推荐：

```text
Rewind
   ↓
Restore Checkpoint
   ↓
Recalculate Diff
   ↓
Run Check
   ↓
Refresh Preview
   ↓
Feedback
```

最终：

```text
已回退到 Turn #12

✓ 3 files restored
✓ npm test
✓ npm lint
✓ Preview ready
```

---

# 21. P1：Evidence Card

把多个 Feedback Provider 的结果聚合成 Evidence。

```text
Evidence

Preview
✓ Ready

Checks
✓ npm test
✓ npm lint

Errors
0

Changes
3 files
```

Evidence 可以挂载到：

```text
Task
Turn
Outcome
```

---

# 22. ⭐ P1：Evidence → Continue

用户可以：

```text
[带当前证据继续]
```

Pieqi 自动把：

```text
changed files
diff summary
check result
error output
preview status
```

整理成下一轮 Agent Context。

例如：

```text
请继续处理。

当前证据：

- src/Login.vue 已修改
- npm test 通过
- npm build 失败
- 错误：xxx
```

这是 Feedback 从：

> 「展示系统」

升级成：

> **Agent Control System**

的关键能力。

---

# 23. P2：Visual Feedback

在 Preview 基础上增加：

### Screenshot

```text
Preview
   ↓
Screenshot
```

手机可以直接查看页面截图。

---

### Browser Console

采集：

```text
console.error
console.warn
```

展示：

```text
Console

✗ 3 errors
⚠ 5 warnings
```

---

### Network Error

如果能够可靠获取：

```text
4xx
5xx
failed request
```

则形成：

```text
Browser Evidence
├── Screenshot
├── Console
└── Network
```

---

# 24. P2：Screenshot → Evidence

例如：

```text
Preview Screenshot
        ↓
Evidence
        ↓
「页面顶部按钮仍然错位」
        ↓
Continue Agent
```

为后续视觉反馈打基础。

---

# 25. P2：Rewind 增强

基础：

```text
Turn Rewind
```

后续可以增加：

```text
File Rewind
```

以及：

```text
Rewind
   ↓
Check
   ↓
Preview
```

自动验证。

---

# 26. P2：Evidence Push

支持将 Task Outcome / Evidence 推送到：

```text
IM
飞书
Webhook
```

不要一开始绑定某一个具体 IM。

建议：

```text
Notification Provider
```

---

# 27. P3：Advanced Visual Feedback

探索：

```text
DOM Inspection
DOM Selector
Region Annotation
Visual Evidence
```

例如用户：

> 「这个按钮有问题」

框选页面区域：

```text
Screenshot
   ↓
Region
   ↓
DOM / Selector
   ↓
Evidence
   ↓
Agent
```

这是未来差异化方向，但不进入当前主路径。

---

# 28. P3：Task Replay

记录：

```text
Task
 ↓
Turn
 ↓
Change
 ↓
Check
 ↓
Evidence
```

最终可以重新查看：

> Agent 是如何一步一步把项目改成现在这样的。

可以为未来 Project Brain 提供数据基础。

---

# 29. ❌ 暂不做

以下能力目前不进入 Feedback RoadMap：

### 完整 Computer Use

原因：

> 会把 Pieqi 从 Agent Control Plane 带向 Computer Agent。

---

### 云桌面

原因：

> 基础设施和产品边界都明显膨胀。

---

### 真终端 PTY 工作台

原因：

> 容易发展成 Web IDE。

---

### 可写 File Explorer

原因：

> 查看文件属于 Feedback；直接编辑文件属于 IDE。

---

### 完整 Browser Automation

Preview 的目标是：

> Observe

不是：

> Control Browser

---

### Task 录屏

截图已经能够覆盖大部分当前验收需求。

录屏：

- 成本高
- 存储重
- 价值暂时不足

---

### 多 Agent Feedback UI

暂不与 Multi-Agent RoadMap 绑定。

先让单 Task Feedback 模型稳定。

---

# 30. 功能优先级总表

| # | 功能 | Priority | 备注 |
|---:|---|:---:|---|
| 1 | Feedback 入口 | P0 | 核心 |
| 2 | FileChange Model | P0 | 核心底座 |
| 3 | Tool → FileChange | P0 | 核心底座 |
| 4 | Turn Change List | P0 | 核心 |
| 5 | Change Summary | ⭐ P0 | 强烈建议新增 |
| 6 | Timeline ↔ Change | P0 | 核心 |
| 7 | Single File Diff | P0 | 核心 |
| 8 | Task Baseline | P0 | 核心 |
| 9 | Checkpoint | P0 | Rewind 基础 |
| 10 | Code Rewind | P0 | 核心控制能力 |
| 11 | Preview Provider | P0 | 可选能力 |
| 12 | Preview Discovery | P0 | 不硬编码启动方式 |
| 13 | Preview Lifecycle | P0 | start/stop/status |
| 14 | Preview Proxy | P0 | Remote/Mobile 必需 |
| 15 | Event/Baseline 双视角 | P1 | 高价值 |
| 16 | Approval → Diff | P1 | 高价值 |
| 17 | Checks Provider | P1 | 高价值 |
| 18 | Check Result | P1 | 高价值 |
| 19 | Check Rerun | P1 | 高价值 |
| 20 | Task Outcome | ⭐ P1 | 强烈建议新增 |
| 21 | Evidence Card | P1 | 差异化 |
| 22 | Evidence → Continue | ⭐ P1 | 核心闭环 |
| 23 | Rewind → Verify | ⭐ P1 | 强烈建议 |
| 24 | Preview Refresh | P1 | 实用 |
| 25 | Preview Attach | P1/P2 | 优化体验 |
| 26 | Screenshot | P2 | Visual Feedback |
| 27 | Browser Console | P2 | Visual Feedback |
| 28 | Network Error | P2 | Visual Feedback |
| 29 | Screenshot → Evidence | P2 | 差异化 |
| 30 | File-level Rewind | P2 | 增强 |
| 31 | Evidence Push | P2 | 多渠道 |
| 32 | DOM Inspection | P3 | 探索 |
| 33 | Region Annotation | P3 | 探索 |
| 34 | Visual Evidence → Agent | P3 | 探索 |
| 35 | Task Replay | P3 | 长期 |
| 36 | Computer Use | ❌ | 不做 |
| 37 | 云桌面 | ❌ | 不做 |
| 38 | Terminal Workbench | ❌ | 不做 |
| 39 | Writable Explorer | ❌ | 不做 |
| 40 | Browser Automation | ❌ | 不做 |
| 41 | Task Recording | ❌ | 暂不做 |

---

# 31. 推荐实施顺序

## Phase 0：Feedback 基础

```text
FileChange
    ↓
Turn Change
    ↓
Change Summary
    ↓
Timeline 联动
```

目标：

> **看见 Agent 改了什么。**

---

## Phase 1：可信变更

```text
Baseline
    ↓
Diff
    ↓
Approval → Diff
```

目标：

> **确认 Agent 到底改了什么。**

---

## Phase 2：运行态反馈

```text
Preview Provider
    ↓
Discovery
    ↓
Lifecycle
    ↓
Proxy
```

目标：

> **看到实际运行效果。**

注意：

Preview 仅在 Task 有运行态验收价值时出现。

---

## Phase 3：可控恢复

```text
Checkpoint
    ↓
Rewind
    ↓
Rewind → Verify
```

目标：

> **改错以后可以安全恢复。**

---

## Phase 4：验证闭环

```text
Checks
    ↓
Evidence
    ↓
Task Outcome
```

目标：

> **判断任务到底有没有完成。**

---

## Phase 5：Agent 干预

```text
Evidence
    ↓
Continue
    ↓
Agent
```

目标：

> **用户不用重新组织上下文，直接带着证据让 Agent 继续。**

---

## Phase 6：Visual Feedback

```text
Preview
   ↓
Screenshot
   ↓
Console
   ↓
Network
   ↓
Visual Evidence
```

目标：

> **从代码反馈进入视觉反馈。**

---

# 32. 最终产品闭环

最终 Pieqi Feedback 体系应该形成：

```text
                         ┌──────────────┐
                         │     Task     │
                         └───────┬──────┘
                                 │
                    ┌────────────┴────────────┐
                    ↓                         ↓
                Timeline                  Baseline
                    │                         │
                    ↓                         ↓
                 Changes ───────────────→ Diff
                    │                         │
                    └────────────┬────────────┘
                                 ↓
                    ┌────────────────────────┐
                    │      Verification       │
                    ├───────────┬────────────┤
                    ↓           ↓            ↓
                 Preview      Checks       Evidence
                    │           │            │
                    └───────────┴─────┬──────┘
                                      ↓
                                Task Outcome
                                      │
                         ┌────────────┼────────────┐
                         ↓            ↓            ↓
                      Approve      Rewind      Continue
                                      │            │
                                      ↓            ↓
                                  Checkpoint     Agent
                                      │            │
                                      └─────┬──────┘
                                            ↓
                                         Changes
```

---

# 33. 最终定位

Pieqi Feedback 最终不应该成为：

> 「一个显示 Agent 输出的面板」。

而应该成为：

> **Agent Task 的 Observe / Verify / Control / Intervene 层。**

核心价值排序：

```text
第一层：Changes
        ↓
      我改了什么？

第二层：Diff
        ↓
      具体怎么改？

第三层：Preview / Checks
        ↓
      做得对不对？

第四层：Rewind
        ↓
      做错怎么办？

第五层：Evidence
        ↓
      有什么证据？

第六层：Intervene
        ↓
      带着证据让 Agent 继续
```

其中：

**P0 的终点不是「Feedback Panel 做出来了」，而是：**

> **Agent 改代码 → 用户手机看到 Changes → 查看 Diff → 必要时 Preview / Checks → 不满意 Rewind → 满意后继续。**

这条闭环成立，即可认为 Pieqi「反馈体系」第一阶段完成。