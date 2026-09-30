# Pieqi 下一阶段综合功能迭代规划 v1.0

> 文档定位：Pieqi 下一阶段产品 / 技术迭代总规划  
> 适用范围：产品规划、技术设计、Sprint 排期、PRD 拆解  
> 核心目标：从「Agent 远程控制工具」进一步演进为「Coding Agent Task Runtime」

---

# 1. Executive Summary

Pieqi 当前已经不再只是一个简单的：

> PWA / IM → Bridge → Coding Agent

远程控制工具。

当前已经具备：

- Multi-Agent
- AgentSession / AgentManager
- Streaming Event
- Approval
- Parallel Task
- Worktree
- Feedback
- Changes / Diff
- Checkpoint / Rewind
- Preview
- Feishu
- PWA
- Vue3 + Vite 前端 V2

因此，下一阶段最重要的问题已经不是：

> 「还能增加什么功能？」

而是：

> **如何把现有能力组织成一个真正可靠的 Agent Task Runtime。**

三份 Agent 分析虽然切入点不同，但最终可以归纳为同一个方向：

```text
                    Pieqi
                      │
                      ▼
             Agent Task Runtime
                      │
       ┌──────────────┼──────────────┐
       ▼              ▼              ▼
    Observe         Verify        Control
       │              │              │
   Event/Timeline   Check/Diff    Approval
   Streaming       Preview       Pause
   History         Outcome       Continue
       │              │              │
       └──────────────┼──────────────┘
                      ▼
                   Recover
                      │
             Checkpoint / Rewind
             Retry / Resume
                      │
                      ▼
                  Continue
```

因此下一阶段不建议继续以：

> 「再增加一个 Agent / 再增加一个 IM / 再增加一个页面」

作为主要迭代方式。

而应该围绕：

> **Task → Observe → Verify → Intervene → Recover → Continue**

构建完整闭环。

---

# 2. 三份 Agent 分析综合评价

## 2.1 总体评分

| 分析来源 | 产品方向 | 技术架构 | 可执行性 | 对当前 Pieqi 匹配度 | 综合 |
|---|---:|---:|---:|---:|---:|
| X Agent | 9.7 | 8.7 | 9.3 | 9.7 | **9.1** |
| Doubao | 9.0 | 9.2 | 9.6 | 9.3 | **8.8** |
| GPT | 8.7 | 9.8 | 8.4 | 9.0 | **8.3** |

---

## 2.2 X Agent

### 核心观点

X Agent 的核心判断是：

> 把「能在手机上驱动 Coding Agent」升级为「能在手机上观察、验证、干预并闭环完成任务」。

重点形成：

```text
Observe
   ↓
Verify
   ↓
Control
   ↓
Intervene
```

然后继续扩展：

```text
Evidence
   ↓
Continue
   ↓
Recover
```

### 优点

非常贴合 Pieqi 当前已有产品形态。

尤其抓住了一个关键问题：

目前 Pieqi 已经能够：

```text
看到 Agent
看到代码变化
审批
Diff
Checkpoint
Rewind
Preview
```

但这些能力如果只是独立功能，就仍然是：

> 「工具箱」

而不是：

> 「完整任务闭环」。

因此 X Agent 对产品方向的判断最准确。

### 最有价值的几个判断

#### ① Feedback 应该继续深化

不是继续增加新的 Feedback 类型，而是：

```text
Changes
 ↓
Diff
 ↓
Check / Preview
 ↓
判断
 ↓
Rewind / Continue
```

形成完整验收闭环。

#### ② Approval 应该和 Diff 结合

不能只是：

```text
Agent：我要修改 xxx
User：Approve
```

而应该：

```text
Agent：准备修改 xxx

       ↓

Prospective Diff

       ↓

User 查看将发生什么

       ↓

Approve / Reject
```

这实际上是 **Human-in-the-loop Runtime** 的核心能力。

#### ③ Evidence 是非常重要的中间层

例如：

```text
Task
 ↓
Agent 修改代码
 ↓
Check
 ↓
Preview
 ↓
Screenshot
 ↓
Evidence
 ↓
Continue
```

这会让 Feedback 不再只是「展示信息」，而变成：

> **Agent 下一轮上下文的一部分。**

### 评价

**9.1 / 10**

X Agent 最适合作为：

> **产品方向决策依据。**

---

# 3. Doubao 分析评价

Doubao 更偏向：

> 「怎么把现在的能力真正做完」。

其特点是给出了较明确的功能拆解：

```text
F-01 Feedback 移动端闭环
F-02 TaskEvent 持久化
F-03 前端 V2
F-04 Check Runner
F-05 Cost / Token
F-06 Agent Watchdog
F-07 Multi-Agent
F-08 Task Replay
F-09 Project 聚合
F-10 Docker / CI
F-11 Visual Evidence
```

其中尤其有价值的是：

## TaskEvent 持久化

Doubao 明确提出：

```text
Task
TaskEvent
Checkpoint
Evidence
```

需要成为持久化对象。

并提出：

```text
SQLite
   ↓
Store Interface
   ↓
Future PostgreSQL
```

以及：

```text
Event ID 全局唯一
Append Only
Timeline 不删除
WebSocket 按 Event ID 去重
```

这对于 Pieqi 非常重要。

因为：

> 没有 Event Store，就很难真正实现 Runtime。

### Restart Recovery

Doubao 还提出：

```text
Running
   ↓
Service Restart
   ↓
Interrupted
```

而不是：

```text
Service Restart
   ↓
偷偷重新执行
```

这个设计非常正确。

### Watchdog

对于 Coding Agent 长任务：

```text
Running
 ↓
长时间无 Event
 ↓
Suspect
 ↓
Notification
```

也是实际使用中非常有价值的能力。

### 评价

**8.8 / 10**

Doubao 最适合作为：

> **下一阶段工程实施与 PRD 拆解依据。**

---

# 4. GPT RoadMap 分析评价

GPT 的优势不是具体功能，而是把 Pieqi 的长期演进路线抽象得比较清晰：

```text
Agent Remote Control
        ↓
Agent Runtime
        ↓
Coding Runtime
        ↓
Agent Workflow
        ↓
Agent Platform
```

其中 P0：

```text
Task Runtime
Event Store
Checkpoint
Intervention
Recovery
```

P1：

```text
Workspace
Task Diff
Timeline / Replay
Task Inbox
Agent Capability
```

P2：

```text
Workflow
Multi-Agent
Strategy
Automation
```

P3：

```text
Task Knowledge
Analytics
Notification
```

这个框架非常适合作为 Pieqi 的长期架构蓝图。

但问题是：

> 它更像 Architecture RoadMap，而不是当前 Sprint 的产品计划。

### 评价

**8.3 / 10**

GPT 最适合作为：

> **Pieqi 长期架构演进依据。**

---

# 5. 综合结论

三份分析不应该三选一。

最佳方案是：

```text
X Agent
   ↓
产品方向

Doubao
   ↓
工程落地

GPT
   ↓
长期架构
```

最终形成：

```text
                 Pieqi
                   │
                   ▼
        Coding Agent Runtime
                   │
        ┌──────────┴──────────┐
        ▼                     ▼
   Product Loop          Runtime Foundation
        │                     │
        ▼                     ▼
 Feedback / Verify       Task / Event
 Approval / Evidence    Checkpoint
 Rewind / Continue      Intervention
 Preview / Outcome      Recovery
        │                     │
        └──────────┬──────────┘
                   ▼
             Coding Runtime
                   │
                   ▼
          Workspace / Git
          Diff / Check
          Replay / Agent
                   │
                   ▼
          Agent Workflow
                   │
                   ▼
          Agent Platform
```

---

# 6. Pieqi 下一阶段真正的核心目标

## 6.1 产品目标

用户能够在手机 / PWA / IM 中完成：

```text
创建 Task
   ↓
Agent 执行
   ↓
实时观察
   ↓
发现需要审批
   ↓
查看将发生的修改
   ↓
Approve / Reject
   ↓
Agent 继续
   ↓
查看实际 Changes
   ↓
Diff
   ↓
Check / Preview
   ↓
判断结果
   │
   ├── 正确 → Evidence → Continue
   │
   └── 错误 → Rewind → Verify → Continue
   ↓
Task Outcome
   ↓
完成
```

最终目标不是：

> 「手机上可以操作 Coding Agent」

而是：

> **「手机上可以完成一次 Coding Agent Task 的完整生命周期管理。」**

---

# 7. 核心产品闭环

建议将 Pieqi 的核心交互模型正式定义为：

```text
Observe
   ↓
Verify
   ↓
Intervene
   ↓
Recover
   ↓
Continue
```

## Observe

用户知道 Agent 正在做什么：

- Streaming
- Timeline
- Tool Call
- Tool Result
- Permission
- Status
- Error

---

## Verify

用户知道：

> Agent 做得对不对。

包括：

- Changes
- Diff
- Checks
- Preview
- Screenshot
- Console
- Network
- Outcome

---

## Intervene

用户可以改变 Agent 的执行：

- Approve
- Reject
- Pause
- Resume
- Append Prompt
- Cancel
- Retry

---

## Recover

任务出现问题后：

- Checkpoint
- Rewind
- Retry
- Resume
- Recovery

---

## Continue

验证完成以后：

```text
Evidence
   ↓
Continue
   ↓
Agent 获得验证结果
   ↓
下一轮执行
```

---

# 8. 下一阶段功能优先级

综合三个 Agent 后，建议重新排序如下。

| 优先级 | 功能 | 价值 | 技术重要性 | 建议 |
|---|---|---:|---:|---|
| P0 | Feedback 完整闭环 | 10 | 9 | **立即做** |
| P0 | Task Runtime 生命周期 | 9.9 | 10 | **立即做** |
| P0 | Event Store | 9.8 | 10 | **立即做** |
| P0 | Intervention | 9.7 | 10 | **立即做** |
| P0 | Checkpoint / Recovery | 9.7 | 10 | **立即做** |
| P0 | Approval → Prospective Diff | 9.6 | 9.5 | **立即做** |
| P0 | Checks | 9.5 | 9 | **立即做** |
| P0 | Task Outcome / Evidence | 9.3 | 8.8 | **立即做** |
| P1 | Watchdog | 9.2 | 8.8 | 尽快 |
| P1 | Workspace / Git Runtime | 9.1 | 9.5 | 第二阶段 |
| P1 | Task Replay | 8.9 | 9 | 第二阶段 |
| P1 | Task Inbox | 8.7 | 8 | 第二阶段 |
| P1 | Agent Capability | 8.5 | 8.5 | 第二阶段 |
| P1 | Screenshot / Console / Network | 8.5 | 7.5 | 第二阶段 |
| P2 | Project 聚合 | 8.2 | 8 | 后续 |
| P2 | Cost / Token | 8.0 | 7 | 后续 |
| P2 | Workflow | 8.8 | 9 | 中期 |
| P2 | Multi-Agent Workflow | 8.7 | 9 | 中期 |
| P2 | Automation | 8.3 | 8.5 | 中期 |
| P3 | Task Knowledge | 8.0 | 9 | 长期 |
| P3 | Analytics | 7.5 | 7 | 长期 |
| P3 | 更多 IM | 6.5 | 5 | 暂缓 |
| P3 | Web IDE | 5 | 10 | **不做** |
| P3 | Cloud Desktop | 4 | 10 | **不做** |
| P3 | 全自动 Browser Agent | 5 | 10 | **暂缓** |

---

# 9. Phase 1：Agent Task Runtime v1

这是下一阶段最重要的版本。

## 目标

建立：

> **可靠运行、可观察、可干预、可恢复的 Agent Task Runtime。**

---

## F-01 Task Runtime

### 目标

把 Task 从：

```text
一条任务记录
```

升级为：

```text
一个持续运行、可控制、可恢复的 Runtime Object
```

### Task 生命周期

建议统一：

```text
CREATED
   ↓
QUEUED
   ↓
RUNNING
   ↓
WAITING
   │
   ├── Approval
   ├── User Input
   └── External Dependency
   ↓
RUNNING
   ↓
COMPLETED
```

异常：

```text
RUNNING
   ↓
FAILED
   ↓
RECOVERABLE
   ↓
RETRY / RESUME / ROLLBACK
```

取消：

```text
RUNNING
   ↓
CANCELING
   ↓
CANCELED
```

服务重启：

```text
RUNNING
   ↓
INTERRUPTED
   ↓
RESUME / CANCEL
```

---

# 10. F-02 Event Store

这是整个 Runtime 的数据基础。

当前：

```text
Agent
 ↓
EventBus
 ↓
WebSocket
 ↓
PWA
```

升级为：

```text
Agent
 ↓
Event
 ↓
Event Store
 ↓
EventBus
 ↓
WebSocket
 ↓
PWA
```

即：

> **Persistence 与 Streaming 解耦。**

---

## 必须持久化

```text
Task
TaskEvent
Checkpoint
Evidence
Intervention
Outcome
```

大对象：

```text
Screenshot
Preview Artifact
Large Log
```

不要直接塞数据库。

采用：

```text
DB
 ↓
File Reference
 ↓
Filesystem / Object Storage
```

---

## Event 原则

### Append Only

Event 一旦写入：

```text
不修改
不删除
```

Timeline 永远保留。

---

### Global Event ID

例如：

```text
event_id
task_id
sequence
timestamp
type
payload
```

前端：

```text
event.id
```

作为去重依据。

---

## Restart

服务重启：

```text
Load Tasks
   ↓
Load Events
   ↓
Running → Interrupted
   ↓
用户决定：
   ├── Resume
   ├── Cancel
   └── Continue
```

禁止：

> 自动静默重新运行 Agent。

---

# 11. F-03 Feedback Runtime Loop

这是产品层 P0。

现有：

```text
Changes
Diff
Checkpoint
Rewind
Preview
```

统一成：

```text
Changes
   ↓
Diff
   ↓
Check
   ↓
Preview
   ↓
Evidence
   ↓
Rewind / Continue
```

---

## Changes

提供两个视角：

### Turn View

```text
当前 Turn 改了什么
```

### Baseline View

```text
Task 开始以来总共改了什么
```

这解决长任务：

> 「到底是哪一轮改了这个文件？」

的问题。

---

# 12. F-04 Approval → Prospective Diff

这是非常建议提前的功能。

当前：

```text
Permission
   ↓
Approve / Reject
```

升级：

```text
Permission
   ↓
解析 Tool Input
   ↓
生成 Prospective Diff
   ↓
用户查看
   ↓
Approve / Reject
```

例如：

```text
Agent：

准备修改：
src/service/UserService.java

预计：
+ 35 lines
- 12 lines

[查看 Diff]

[Reject] [Approve]
```

---

## 重要意义

这不是普通 UI 优化。

它实际上建立：

> **Human-in-the-loop Control Boundary**

用户批准的不是：

> 「Agent 可以执行工具」

而是：

> **「我知道它准备做什么，并允许它做。」**

---

# 13. F-05 Checks

目标：

从：

> 「看日志判断 Agent 做得对不对」

升级为：

> **「有机器验证结果证明 Agent 做得对不对。」**

---

## Check 类型

第一阶段：

```text
Test
Build
Lint
Type Check
```

第二阶段：

```text
Security
API Test
Integration Test
Custom Script
```

---

## Provider 模型

不要写死：

```text
Java = mvn test
Node = npm test
```

而应该：

```text
CheckProvider
   ├── Maven
   ├── Gradle
   ├── NPM
   ├── Go
   ├── Python
   └── Custom
```

---

## 状态

```text
NOT_RUN
RUNNING
PASSED
FAILED
SKIPPED
```

---

# 14. F-06 Task Outcome

任务结束后，不应该要求用户重新看完整 Timeline。

提供：

```text
Task Outcome
```

例如：

```text
✓ Completed

修改文件：8
新增：2
删除：1

Tests：Passed
Build：Passed

主要变更：
- 增加用户认证
- 修改登录流程
- 增加单元测试

风险：
- xxx API 未覆盖

建议：
继续进行集成测试
```

---

# 15. F-07 Evidence

Evidence 是 Pieqi 后续非常重要的数据结构。

建议统一：

```text
Evidence
├── Check
├── Diff
├── Preview
├── Screenshot
├── Console
├── Network
├── User Observation
└── Agent Result
```

---

## Evidence → Continue

例如：

```text
Preview
 ↓
用户发现按钮异常
 ↓
Screenshot
 ↓
Evidence
 ↓
Continue
 ↓
Agent：
「根据截图，按钮在移动端发生溢出……」
```

这会让：

> Feedback

真正成为：

> **Agent Context。**

---

# 16. F-08 Checkpoint / Rewind / Recovery

Checkpoint 不应该只是一个按钮。

它应该属于 Runtime Recovery。

```text
Checkpoint
     │
     ├── Snapshot
     ├── Event Position
     └── Workspace State
```

---

## Rewind

原则：

> 只回退 Agent 实际修改过的内容。

绝不能：

> 覆盖用户在 Task 开始前已经存在的未提交修改。

---

## Rewind 后

不要删除历史。

应该产生：

```text
Event:
REWIND
```

Timeline：

```text
Turn 1
Turn 2
Turn 3
Rewind → Checkpoint #2
Turn 4
```

这样整个过程仍然可审计。

---

# 17. F-09 Watchdog

针对长时间 Agent Task。

例如：

```text
RUNNING
 ↓
15 min 无 Event
 ↓
SUSPECTED
 ↓
Notification
```

---

## Agent Health

建议：

```text
ONLINE
DEGRADED
OFFLINE
```

Task：

```text
RUNNING
WAITING
STALLED
FAILED
```

不要把：

> Agent 没有输出

直接等价成：

> Agent 已经死了。

应该区分：

```text
No Event
No Process
Bridge Error
Agent Error
External Wait
```

---

# 18. Phase 1 最终架构

完成后，建议形成：

```text
                 ┌───────────────┐
                 │      PWA      │
                 └───────┬───────┘
                         │
                    WebSocket
                         │
                 ┌───────▼───────┐
                 │   Event API    │
                 └───────┬───────┘
                         │
              ┌──────────▼──────────┐
              │      EventBus       │
              └──────────┬──────────┘
                         │
          ┌──────────────▼──────────────┐
          │        Task Runtime         │
          │                              │
          │ Lifecycle                    │
          │ Intervention                │
          │ Recovery                    │
          │ Checkpoint                  │
          │ Outcome                     │
          └───────┬───────────┬─────────┘
                  │           │
          ┌───────▼─────┐ ┌──▼──────────┐
          │ Event Store │ │ AgentAdapter│
          └─────────────┘ └──┬──────────┘
                              │
                    ┌─────────┼─────────┐
                    ▼         ▼         ▼
                 Claude     Qoder     Codex
```

---

# 19. Phase 2：Coding Runtime

当 Task Runtime 稳定以后，再进一步从：

> Agent Runtime

升级：

> Coding Runtime

---

## F-10 Workspace Runtime

Task 与代码环境正式绑定：

```text
Task
 ↓
Workspace
 ↓
Repository
 ↓
Branch
 ↓
Worktree
 ↓
Git State
```

支持：

- Repository
- Branch
- Worktree
- Git status
- Uncommitted changes
- Task workspace

---

# 20. F-11 Task Timeline / Replay

有了 Event Store 后自然出现：

```text
Task
 ↓
Timeline
 ↓
Replay
```

Timeline：

```text
User
 ↓
Agent
 ↓
Tool
 ↓
Tool Result
 ↓
Approval
 ↓
Code Change
 ↓
Check
 ↓
Preview
 ↓
User
```

Replay 的意义不是单纯「重新播放动画」。

而是：

> **回答 Agent 到底是怎么把代码改成现在这样的。**

---

# 21. F-12 Task Inbox

随着 Task 增多，需要统一任务中心。

```text
Task Inbox
```

分类：

```text
Running
Waiting
Needs Approval
Failed
Interrupted
Completed
```

核心目标：

> 用户打开 Pieqi 后，不需要进入每个项目寻找 Agent。

而是直接看到：

> **哪些 Agent Task 需要我处理。**

---

# 22. F-13 Agent Capability / Catalog

统一：

```text
Claude Code
Codex
Qoder
ACP Agent
其他 Agent
```

但这里不要过早追求：

> 所有 Agent 完全一致。

应该采用：

```text
Common Runtime Contract
          +
Agent Specific Capability
```

例如：

```text
Agent Capability

Streaming       ✓
Approval        ✓
Pause           ?
Resume          ✓
Checkpoint      Pieqi
Rewind          Pieqi
Screenshot      ?
MCP             ✓
```

这样 Pieqi 的抽象会更加真实。

---

# 23. F-14 Visual Evidence

这是前端任务场景非常有价值的差异化能力。

```text
Preview
 ↓
Screenshot
 ↓
用户框选区域
 ↓
Annotation
 ↓
Evidence
 ↓
Agent
```

例如：

```text
[截图]

        ┌─────────────┐
        │ Button 错位 │ ← 用户标注
        └─────────────┘

问题：
移动端按钮向右溢出
```

然后：

```text
Continue
```

Agent 直接获得：

```text
Screenshot
+
Region
+
User Description
```

---

# 24. Phase 3：Agent Workflow

当单 Task Runtime 成熟后，再做 Workflow。

从：

```text
One Task
   ↓
One Agent
```

升级：

```text
Workflow
   │
   ├── Planner
   │
   ├── Coder
   │
   ├── Tester
   │
   └── Reviewer
```

---

## Workflow

例如：

```text
需求
 ↓
Planner
 ↓
Coder
 ↓
Test
 ↓
Reviewer
 ↓
Human Approval
 ↓
Merge
```

每个 Step 本质上仍然是：

```text
Task Runtime
```

所以：

> **先做 Task Runtime，再做 Workflow。**

不要反过来。

---

# 25. Multi-Agent

Multi-Agent 不应该成为当前 P0。

因为：

```text
Multi-Agent
```

会放大：

- Task State
- Event
- Context
- Workspace
- Recovery
- Coordination

复杂度。

正确顺序：

```text
Single Agent Runtime
        ↓
Reliable Task Runtime
        ↓
Workflow
        ↓
Multi-Agent
```

---

# 26. Automation

最终可以支持：

```text
Schedule
Git Push
GitHub/GitLab Webhook
CI Failure
Issue
Jira
```

触发：

```text
Create Task
 ↓
Workflow
 ↓
Agent
 ↓
Verify
 ↓
Outcome
 ↓
Notification
```

此时 Pieqi 才开始从：

> 人主动驱动 Agent

变成：

> **系统自动驱动 Agent。**

---

# 27. Phase 4：Agent Platform

长期方向：

```text
Task Knowledge
Project Knowledge
Analytics
Cost
Policy
Automation
Multi-Agent
```

最终形成：

```text
                 Pieqi Platform
                       │
        ┌──────────────┼──────────────┐
        ▼              ▼              ▼
   Task Runtime    Workflow       Knowledge
        │              │              │
        ▼              ▼              ▼
    Coding         Multi-Agent    Project Brain
    Runtime
        │
        ▼
   Agent Catalog
```

---

# 28. Project Knowledge

长期可以沉淀：

```text
Task
 ↓
Decision
 ↓
Code Change
 ↓
Check
 ↓
Outcome
```

形成：

```text
Project
 ├── Tasks
 ├── Changes
 ├── Decisions
 ├── Agent Runs
 ├── Failures
 └── Knowledge
```

最终回答：

> 「这个项目过去半年 Agent 都做过什么？」

> 「这个模块为什么这么改？」

> 「之前有没有解决过类似问题？」

---

# 29. 暂时明确不做的事情

## 29.1 Web IDE

不建议把 Pieqi 变成：

```text
VSCode Web
```

原因：

- 开发量巨大
- 与 Pieqi 核心价值无关
- 容易失去产品定位

---

## 29.2 Cloud Desktop

不做：

```text
Remote Desktop
```

Pieqi 不应该成为：

> 云电脑。

---

## 29.3 Full Browser Automation

暂时不做：

```text
完整 Computer Use Platform
```

浏览器能力应该首先服务于：

```text
Preview
Screenshot
Evidence
Verify
```

而不是成为独立产品。

---

## 29.4 大量 IM Channel

暂时不应该优先：

```text
飞书
→ 微信
→ 企业微信
→ Slack
→ Telegram
→ Discord
```

因为：

> Channel 不是 Pieqi 当前核心竞争力。

应该先让：

```text
PWA
+
当前 IM
```

把 Runtime 闭环跑通。

---

# 30. 推荐实施顺序

不要按照「功能列表」线性开发。

建议按照 Runtime 依赖关系：

```text
第一层：Task Runtime
        │
        ▼
第二层：Event Store
        │
        ▼
第三层：Intervention
        │
        ▼
第四层：Checkpoint / Recovery
        │
        ▼
第五层：Feedback
        │
        ├── Changes
        ├── Diff
        ├── Checks
        ├── Preview
        └── Evidence
        │
        ▼
第六层：Outcome
        │
        ▼
第七层：Watchdog
        │
        ▼
第八层：Workspace
        │
        ▼
第九层：Replay
        │
        ▼
第十层：Workflow
        │
        ▼
Multi-Agent / Automation / Knowledge
```

---

# 31. 建议 Sprint 拆分

## Sprint 1：Runtime 地基

目标：

> Task 真正成为 Runtime。

实现：

- Task State Machine
- Task Lifecycle
- Task / Session / Agent 关系明确
- Event Model
- Event ID
- Event Store 接口
- SQLite 实现

---

## Sprint 2：Event + Recovery

实现：

- TaskEvent 持久化
- WebSocket Event Replay
- Event Dedup
- Restart Recovery
- Interrupted
- Resume
- Retry
- Cancel

---

## Sprint 3：Feedback 闭环

实现：

- Changes Turn View
- Changes Baseline View
- Diff
- Check
- Preview
- Rewind
- Continue

形成：

```text
Changes
 → Diff
 → Check
 → Preview
 → Rewind / Continue
```

---

## Sprint 4：Human-in-the-loop

实现：

- Approval
- Prospective Diff
- Reject
- Pause
- Resume
- Append Prompt
- Intervention Event

---

## Sprint 5：Evidence / Outcome

实现：

- Evidence
- Evidence → Continue
- Task Outcome
- Outcome Push
- Evidence Push
- Timeline integration

---

## Sprint 6：稳定性

实现：

- Watchdog
- Agent Health
- Bridge Health
- Timeout
- Stalled Task
- Notification
- Recovery

---

# 32. 第一阶段 Definition of Done

Phase 1 不应该以：

> 「功能都做出来了」

作为完成标准。

应该以真实任务闭环作为验收。

---

## DoD-01

手机上创建 Task：

```text
Create
 ↓
Running
 ↓
Streaming
```

---

## DoD-02

Agent 请求敏感操作：

```text
Permission
 ↓
Prospective Diff
 ↓
Approve
 ↓
Continue
```

---

## DoD-03

Agent 修改代码：

```text
Changes
 ↓
Diff
```

---

## DoD-04

用户验证：

```text
Check
+
Preview
```

---

## DoD-05

发现问题：

```text
Rewind
 ↓
Verify
 ↓
Continue
```

---

## DoD-06

服务重启：

```text
Running
 ↓
Interrupted
 ↓
历史 Event 保留
 ↓
Resume
```

---

## DoD-07

任务结束：

```text
Outcome
+
Evidence
```

---

## DoD-08

长时间无响应：

```text
Watchdog
 ↓
Suspect
 ↓
Notification
```

---

# 33. 最终产品模型

完成上述阶段后，Pieqi 的核心对象应该逐渐稳定为：

```text
Project
   │
   ├── Workspace
   │
   └── Task
         │
         ├── Agent
         │
         ├── Session
         │
         ├── Events
         │
         ├── Interventions
         │
         ├── Checkpoints
         │
         ├── Changes
         │
         ├── Checks
         │
         ├── Evidence
         │
         ├── Outcome
         │
         └── Recovery
```

这比单纯：

```text
Task
Session
Agent
```

更加完整。

---

# 34. Pieqi 的核心竞争力应该是什么

不是：

> 「我支持 Claude Code。」

也不是：

> 「我支持手机控制 Coding Agent。」

更不是：

> 「我支持多少种 IM。」

而应该是：

> **Pieqi 提供一个让 Coding Agent 可观察、可验证、可干预、可恢复的 Task Runtime。**

即：

```text
Coding Agent
      │
      ▼
┌─────────────────────────┐
│      Pieqi Runtime      │
│                         │
│ Observe                 │
│ Verify                  │
│ Intervene               │
│ Recover                 │
│                         │
└─────────────────────────┘
      │
      ▼
Mobile / PWA / IM
```

---

# 35. 最终 RoadMap

```text
                    CURRENT
                       │
                       ▼
             Agent Remote Control
                       │
                       │
                 Phase 1
                       ▼
             ┌─────────────────┐
             │ Agent Runtime   │
             │                 │
             │ Task            │
             │ Event           │
             │ Intervention    │
             │ Checkpoint      │
             │ Recovery        │
             │ Feedback        │
             └────────┬────────┘
                      │
                 Phase 2
                      ▼
             ┌─────────────────┐
             │ Coding Runtime  │
             │                 │
             │ Workspace       │
             │ Git             │
             │ Diff            │
             │ Check           │
             │ Replay          │
             └────────┬────────┘
                      │
                 Phase 3
                      ▼
             ┌─────────────────┐
             │ Agent Workflow  │
             │                 │
             │ Workflow        │
             │ Multi-Agent     │
             │ Strategy        │
             │ Automation      │
             └────────┬────────┘
                      │
                 Phase 4
                      ▼
             ┌─────────────────┐
             │ Agent Platform  │
             │                 │
             │ Knowledge       │
             │ Analytics       │
             │ Policy          │
             │ Project Brain   │
             └─────────────────┘
```

---

# 36. 最终建议

如果现在只能选 **5 件事情**继续做：

### 第一优先：Task Runtime

把：

```text
Task / Session / Agent
```

关系和生命周期彻底理顺。

### 第二优先：Event Store

让：

```text
实时事件
```

升级为：

```text
可持久化、可恢复、可回放的 Agent Execution History
```

### 第三优先：Feedback 闭环

把：

```text
Changes
Diff
Checkpoint
Rewind
Preview
```

真正串起来。

### 第四优先：Intervention

尤其：

```text
Approval
+
Prospective Diff
+
Pause / Resume
+
Append Prompt
```

建立真正的 Human-in-the-loop。

### 第五优先：Recovery

让：

```text
失败
中断
错误
重启
```

都能够恢复，而不是重新开始。

---

# 37. 一句话战略

> **下一阶段不要继续堆「Agent、IM、页面」，而应该把 Pieqi 从 Remote Control 真正升级成 Agent Task Runtime。**

产品闭环：

```text
Task
 ↓
Observe
 ↓
Verify
 ↓
Intervene
 ↓
Recover
 ↓
Continue
 ↓
Outcome
```

技术地基：

```text
Task Runtime
+
Event Store
+
Checkpoint
+
Intervention
+
Recovery
```

长期演进：

```text
Agent Runtime
      ↓
Coding Runtime
      ↓
Agent Workflow
      ↓
Agent Platform
```

**这应该成为 Pieqi 接下来一段时间所有功能取舍的总原则。**