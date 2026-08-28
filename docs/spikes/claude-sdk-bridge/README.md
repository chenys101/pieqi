# claude-sdk-bridge — P0 Spike 存档

> **性质**：P0 spike 验证记录（2026-08-20），**非产品代码**。正式桥实现按方案规划落在 `services/claude-sdk-bridge/`，本目录仅存档当时验证用的脚本与结论，不进构建、不被任何 Go 代码引用。

## 验证了什么

对官方 `@anthropic-ai/claude-agent-sdk` 的三个关键假设（见 `docs/multi-agent-evaluation.md` §8）：

| 脚本 | 验证点 | 结果 |
|------|--------|------|
| `spike/u1_multiturn.mjs` | U1 同进程多轮流式 + turn 边界 + 增量流式 | PASS |
| `spike/u2_permission.mjs` | U2 canUseTool 权限挂起与释放 | PASS |
| `spike/u3_resume.mjs` | U3 CLI 子进程崩溃后 resume | PASS |

结论与桥构建的 8 条关键约束（`includePartialMessages` 必需、正文双通道需去重、崩溃是 throw、Windows 工具名是 PowerShell 等）见评估文档 §8.4。

## 怎么复跑

```bash
cd docs/spikes/claude-sdk-bridge
npm install          # 再生 node_modules（已 gitignore）
npm run spike:u1     # 或 spike:u2 / spike:u3
```

- 依赖：Node ≥20、claude CLI（`~/.claude` 已登录、代理可达）
- 环境：SDK `@anthropic-ai/claude-agent-sdk@0.3.237`，claude CLI 2.1.229（Windows native）
- 每个脚本内置 PASS/FAIL 断言 + 非零退出码，复跑即验证
- `.spike-cwd/` 为 scratch 工作目录（session 文件实际落在 `~/.claude/projects/`）
