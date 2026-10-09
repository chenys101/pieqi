# 图片只留元数据，base64 绝不进任务文件

## 背景

pieqi 要让手机端能"发一张图给 agent 看"（截图报 bug、拍张照问问题）。ACP 协议对此有原生支持：客户端在 `session/prompt` 里塞 `ContentBlock::Image`（内联 base64），对端 agent 在 `Initialize` 应答里用 `promptCapabilities.image` 声明自己收不收。

落地时有一个必须当场定死的取舍：**图片本体放哪儿**。

这不是风格问题，是量级问题：

| 事实 | 数字 | 出处 |
|---|---|---|
| 一张手机照片 | 2–8 MB | 原图；base64 再涨 ~1/3 |
| 任务文件要整体序列化 | `~/.pieqi/tasks/<id>.json` | 每次变更 `os.Rename` 原子写 |
| 列表快照曾经多大 | **11.25 MB / 300ms+**（24 个任务） | 就是 `Task.Events` 全量下发导致的，见 `model.TaskSummary` 注释 |
| 因此列表摘掉了 events | `TaskSummary` 遮蔽 `Events` | 同上 |

把图片 base64 写进 `TaskEvent` 的话，每条带图消息会给任务文件加几 MB，而 `task_updated` 会把整份 Task 在 WS 上全量推给每个连接的客户端 —— **每发一轮图，所有在线设备都要下载一遍那几 MB**。这正是当初把 events 从列表摘掉的那个坑，只是量级更大（那时是几十 KB 的文本，这是几 MB 的二进制）。

另一个不能忽略的点：**能收图这件事不是 pieqi 说了算**。它取决于对端 agent 在握手时的声明。qodercli v1.1.64 实测声明 `promptCapabilities.image = true`（见 `acp initialized` 日志的 `prompt_image` 字段）；claude 桥的 prompt 接口只有文本，恒为 false。同一条链路上两种 agent 能力不同。

## 决策

**图片本体只在内存过一次手；任务里只存元数据。**

分三层落地：

1. **传输**：`agent.ImageInput{Data, MimeType}`（`Data` 是**纯 base64**，不带 `data:` 前缀）。ACP 的 `ContentBlockImage.data` 就是这个形态；带前缀会让对端把前缀也当数据解码。
2. **内存暂存**：首轮的图走 `TaskRunner.pendingImages`（`map[taskID][]ImageInput`），**取后即清**（`takePendingImages`）。续问的图直接作为参数传到那一轮。两者都不经过 `model.Task`。
3. **持久化**：只落 `model.TaskImage{MimeType, Bytes, Hash}` 到 `TaskEvent.Images`。`Bytes` 是**解码后**字节数（拿 base64 长度判大小会在边界放进来过大的图），`Hash` 是解码后内容的 SHA-256。

配套的两条纪律：

- **能力不猜**：`ACPAgent.SupportsImagePrompt()` 只读对端声明；未握手时返回 false（保守侧）。前端据 `GET /api/tasks/:id/capabilities` 决定显不显示加图入口 —— 摆一个按了必然失败的按钮，比没有这个功能更糟。
- **不支持就明说**：对端没声明、或走 claude -p / bridge 路径时，带图请求**明确报错**（`agent.ErrImageNotSupported`，API 层翻成 4xx），绝不静默丢图继续发文本。静默丢图会让用户以为 agent 看过图了，而它会对着没图的上下文说些不相干的话 —— 那种失败最难归因。

## 后果

**好的**：

- 任务文件与 WS 推送的大小**与是否带图无关**，从结构上成立（图片压根没有进入 `model.Task` 的路径），而不是靠每处代码自觉。
- 前端能显示"这条消息带了 N 张图、多大"（元数据够用），且任务 JSON 可长期保存。
- 同一张图重复发出时，`Hash` 相同 —— 为日后接内容寻址存储（dsh-acp 的 attachment store 就是那个形态）留了回查键。

**代价**：

- **历史消息的图片不可回看**：任务里只有元数据，没有本体；刷新页面后旧消息显示不出缩略图。当前接受这个代价（要看图就去问 agent 再发一次 / 看 Visual 截图的独立链路）。
- 校验要在客户端与服务端做两遍（大小、mime、base64 形状），且两边的白名单必须一致（`web/src/features/session/imageAttach.ts` 的 `ALLOWED_IMAGE_TYPES` ↔ `internal/agent/acp.go` 的 `imageMimeWhitelist`）。不一致的表现是"选图后发送失败"，错误信息离根因很远 —— 两条白名单旁都写了指向对方的注释。

**验证方式**（2026-10-09 实测）：

- 真机：`web/scripts/_measure-usage-images.mjs` 量出加图按钮在三档视口都在输入框内、`accept` 与后端白名单一致。
- 端到端：隔离实例 + qoder（ACP），带图续问后服务端日志出现 `prompt: [ [Object], [Object] ]`（对比纯文本轮的 `[ [Object] ]`）—— 文本块 + 图片块确实按序到了 agent。
- 负向：任务 JSON 全文搜索 base64 片段，**搜不到**（`TestStartRich_ImagesNotInTaskFile` 守着这条）。
