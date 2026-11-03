/** 拆分提案按泳道分组：design → fe → be → test（+ other）。 */

export type LaneId = 'design' | 'fe' | 'be' | 'test' | 'other'

export interface LaneMeta {
  id: LaneId
  label: string
  hint: string
  /** 写入 ProposedTaskNode.role 的后端枚举 */
  role: string
}

export const LANES: LaneMeta[] = [
  { id: 'design', label: 'design', hint: '摸底 / 方案 / 文档', role: 'design' },
  { id: 'fe', label: 'fe', hint: '前端 UI / 文案', role: 'frontend' },
  { id: 'be', label: 'be', hint: 'API / 联调', role: 'backend' },
  { id: 'test', label: 'test', hint: '单测 / e2e / 验收', role: 'test' },
  { id: 'other', label: 'other', hint: '其它角色', role: 'other' },
]

/** role / 别名 → 泳道 */
export function roleToLane(role?: string): LaneId {
  const r = (role || '').trim().toLowerCase()
  switch (r) {
    case 'design':
    case 'docs':
      return 'design'
    case 'frontend':
    case 'fe':
      return 'fe'
    case 'backend':
    case 'be':
      return 'be'
    case 'test':
      return 'test'
    case 'review':
    case 'fix':
    case 'other':
      return 'other'
    default:
      return r ? 'other' : 'design'
  }
}

export function laneMeta(id: LaneId): LaneMeta {
  return LANES.find((l) => l.id === id) || LANES[0]
}

export function isLongPrompt(text: string, charLimit = 72, lineLimit = 2): boolean {
  const s = text || ''
  if (s.length > charLimit) return true
  return s.split(/\n/).length > lineLimit
}

export function promptLenLabel(text: string): string {
  const s = text || ''
  const lines = s.split(/\n/).length
  return lines > 1 ? `${s.length} 字 · ${lines} 行` : `${s.length} 字`
}
