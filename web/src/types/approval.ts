// Approval 模型（方案 §9.4）。
// Backend First：当前协议只支持 approve / deny 两种动作；
// allow_always 需要后端协议演进后再放开（方案 §3.1）。

export type ApprovalChoice = 'approve' | 'deny'

export interface ApprovalRequest {
  id: string
  taskId: string
  /** approval=权限审批（批准/拒绝）；choice=多选提问（已废弃，兜底提示文本回复） */
  kind: 'approval' | 'choice'
  tool?: string
  summary: string
  options: string[]
  createdAt: string
}
