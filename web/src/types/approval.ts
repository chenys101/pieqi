// Approval 模型（方案 §9.4）。
// Backend First：当前协议只支持 approve / deny 两种动作；
// allow_always 需要后端协议演进后再放开（方案 §3.1）。

export type ApprovalChoice = 'approve' | 'deny'

/** 审批风险分级。等级由**后端**判定（复用 riskLevelKinds），前端不自己算 —— 见 riskOf()。 */
export type RiskLevel = 'L0' | 'L1' | 'L2' | 'L3'

/** 分级 → 一句话说明「这一档意味着什么」。文案与设置页组①的锁定行同源。 */
export const RISK_LABELS: Record<RiskLevel, string> = {
  L0: '只读探测',
  L1: '写入',
  L2: '执行命令',
  L3: '破坏性操作',
}

/**
 * 归一化风险等级：**缺省一律按 L2**。
 *
 * 这不是保守过头的默认值，而是与后端 `RiskOfKind` 同一个判据 ——
 * 分级表的作用是**枚举**已知操作，没枚举到的东西若按低风险显示，
 * 等于让"我们不认识的操作"悄悄拿到一张弱强度的卡。
 * choice 类决策（Claude 文本提问）没有工具语义，也走这里。
 */
export function riskOf(risk: string | undefined | null): RiskLevel {
  return risk === 'L0' || risk === 'L1' || risk === 'L3' ? risk : 'L2'
}

/**
 * 排序权重（越大越该排在前面）。SPEC §6.2：待审批按风险降序，同风险按时间升序
 * （**等得最久的在前** —— 它最有可能是被遗忘的那个，而不是最新的那个更紧急）。
 */
export function riskWeight(risk: string | undefined | null): number {
  switch (riskOf(risk)) {
    case 'L3':
      return 3
    case 'L2':
      return 2
    case 'L1':
      return 1
    default:
      return 0
  }
}

export interface ApprovalRequest {
  id: string
  taskId: string
  /** approval=权限审批（批准/拒绝）；choice=多选提问（已废弃，兜底提示文本回复） */
  kind: 'approval' | 'choice'
  tool?: string
  /** 风险分级；缺省（旧任务 / choice）由 riskOf() 兜底为 L2 */
  risk?: RiskLevel
  summary: string
  options: string[]
  createdAt: string
}
