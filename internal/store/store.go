// Package store 定义 Collaboration Plane / 短路径 Task 的统一持久化接口（ADR-0022）。
//
// 业务代码只依赖本接口；默认实现是 SQLite（internal/store/sqlite），预留 PG。
// 禁止 JSON Task 文件与库并行双真相。
package store

import "pieqi/internal/model"

// TaskRepo 短路径与长路径共用的 Task 持久化。
type TaskRepo interface {
	UpsertTask(t *model.Task) error
	GetTask(id string) (*model.Task, bool, error)
	ListTasks() ([]*model.Task, error)
	DeleteTask(id string) error
}

// RequirementRepo 需求 CRUD（P0）；split/confirm 等编排在更高层。
type RequirementRepo interface {
	UpsertRequirement(r *model.Requirement) error
	GetRequirement(id string) (*model.Requirement, bool, error)
	ListRequirements() ([]*model.Requirement, error)
	DeleteRequirement(id string) error
}

// Store 统一入口。Close 释放底层连接。
type Store interface {
	TaskRepo
	RequirementRepo
	Close() error
}
