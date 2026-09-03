# 视觉反馈用独立 Node Playwright 服务采集（仿 claude-sdk-bridge）

Screenshot / Browser Console / Network 由一个 Pieqi 按需 spawn 的 node 服务（services/visual-capture，Playwright）完成，Go 侧 VisualCaptureManager 经 HTTP 调用。选 Playwright 因为它已是 web 依赖（@playwright/test）且能同时覆盖截图 + console + network（CDP）；独立 node 服务复用 claude-sdk-bridge 的 spawn/健康/清理模式，避免把 Go 与浏览器自动化耦合。曾考虑 Go chromedp（纯 Go）与页面注入脚本：前者引入新依赖且生态弱于 Playwright，后者无法独立截图。代价：需要安装 chromium 浏览器二进制。
