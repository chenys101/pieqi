// Package web 嵌入 PWA 前端产物（web/dist）。
// 产物由 `npm run build`（vite，在 web/ 目录）生成。
package web

import "embed"

//go:embed all:dist
var Dist embed.FS
