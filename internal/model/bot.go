package model

import "time"

// BotRole 机器人角色。
//
// 特权归属于**绑定的飞书账号（人）**，机器人只是承载与转达者
// （见 docs/ui-design/IMPLEMENTATION-PLAN.md §4.2）：
// role=admin 表示这台机器人受理管理员特权命令（隧道 / API），
// member 收到特权命令一律回「无权操作」。
type BotRole string

const (
	BotRoleAdmin  BotRole = "admin"
	BotRoleMember BotRole = "member"
)

// Bot 一台 IM 机器人 —— pieqi 侧的一条绑定记录，而非飞书开放平台对象。
//
// 两个概念必须分开：
//   - **飞书应用**：开放平台对象（app_id / app_secret / 事件回调），存在于飞书侧
//     与本地凭据文件；
//   - **机器人**：本结构，引用某个渠道应用并承载本地属性（角色 / 预设提示词）。
//
// app_secret **不在本结构里** —— 它留在 per-bot 的凭据文件
// （~/.pieqi/bots/<id>.json，0600）中，由 internal/larkreg 读写，
// 永不进入 API 响应。
type Bot struct {
	ID        string    `json:"id"`         // 本地稳定 id（生成）
	Channel   Channel   `json:"channel"`    // lark / wecom / wechat —— 属性，不是分组
	Name      string    `json:"name"`       // 显示名，如 "飞书 · 管理员"
	Role      BotRole   `json:"role"`       // admin / member
	SysPrompt string    `json:"sys_prompt"` // 预设提示词（D2 的落点）
	AppID     string    `json:"app_id"`     // 渠道侧应用标识（可安全下发）
	CreatedAt time.Time `json:"created_at"` // 列表按此排序
}
