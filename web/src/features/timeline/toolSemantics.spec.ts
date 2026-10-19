// 工具语义表单测。
//
// 守两条判据（见 toolSemantics.ts 顶部）：
//   ① 本表**只管展示**，不参与审批 —— 所以这里不测"放行/不放行"，
//      而要测"能认出什么、认不出时返回 undefined（不猜）"。
//   ② 认不出就不猜：任何未知工具必须返回 undefined 走通用卡，
//      绝不因为名字里含 "goal"/"task" 之类的子串就归类。
import { describe, expect, it } from 'vitest'
import { semanticOf, headlineOf, TOOL_SEMANTICS, SEMANTIC_LABEL } from './toolSemantics'

describe('semanticOf：能认出语义的工具', () => {
  it('goal 类（dsh 的小写命名）', () => {
    expect(semanticOf('create_goal')).toBe('goal')
    expect(semanticOf('update_goal')).toBe('goal')
    expect(semanticOf('get_goal')).toBe('goal')
    expect(semanticOf('clear_goal')).toBe('goal')
  })

  it('todo / plan / subagent / jobs', () => {
    expect(semanticOf('todo_write')).toBe('todo')
    expect(semanticOf('exit_plan_mode')).toBe('plan')
    expect(semanticOf('Task')).toBe('subagent') // claude 的子代理工具就叫 Task
    expect(semanticOf('subagent')).toBe('subagent')
    expect(semanticOf('job_kill')).toBe('jobs')
  })

  it('大小写不敏感（claude 首字母大写 / dsh 全小写）', () => {
    expect(semanticOf('Create_Goal')).toBe('goal')
    expect(semanticOf('TODO_WRITE')).toBe('todo')
  })

  it('qoder 名字后拼参数时取首词', () => {
    expect(semanticOf('Task 帮我查一下这个函数')).toBe('subagent')
    expect(semanticOf('exit_plan_mode 如上')).toBe('plan')
  })
})

describe('semanticOf：认不出就不猜', () => {
  it('未知工具返回 undefined（走通用卡）', () => {
    expect(semanticOf('Bash')).toBeUndefined()
    expect(semanticOf('SomeBrandNewTool')).toBeUndefined()
    expect(semanticOf('read')).toBeUndefined() // 只读工具不需要语义卡，用通用卡即可
  })

  it('**不按子串猜**：名字里含 goal/task/plan 但不是那些工具', () => {
    // 这条最重要：启发式归类一旦生效，用户会看到一张写着"目标"的卡片，
    // 实际却是一次别的操作 —— 比不渲染更糟。
    expect(semanticOf('get_goals_for_project')).toBeUndefined()
    expect(semanticOf('taskkill')).toBeUndefined()
    expect(semanticOf('replan_file')).toBeUndefined()
    expect(semanticOf('todoist_sync')).toBeUndefined()
  })

  it('空名不崩', () => {
    expect(semanticOf('')).toBeUndefined()
    expect(semanticOf('   ')).toBeUndefined()
  })
})

describe('headlineOf：取最该展示的一句', () => {
  it('goal 取 objective', () => {
    expect(headlineOf('goal', { objective: '把 e2e 基线补齐' })).toBe('把 e2e 基线补齐')
  })

  it('goal 没有 objective 时退回 action', () => {
    expect(headlineOf('goal', { action: 'pause', goal_id: 'g1' })).toBe('pause')
  })

  it('todo 汇总成"N 项 · M 完成"', () => {
    const todos = [
      { content: 'a', status: 'completed' },
      { content: 'b', status: 'pending' },
      { content: 'c', status: 'done' },
    ]
    expect(headlineOf('todo', { todos })).toBe('3 项 · 2 完成')
  })

  it('plan 只取首行（方案正文可能很长）', () => {
    expect(headlineOf('plan', { plan: '第一步：梳理现状\n第二步：改代码' })).toBe('第一步：梳理现状')
  })

  it('取不到时返回空串（不编造）', () => {
    expect(headlineOf('goal', {})).toBe('')
    expect(headlineOf('todo', {})).toBe('')
    expect(headlineOf('todo', { todos: [] })).toBe('')
    expect(headlineOf('subagent', null)).toBe('')
  })
})

describe('表的一致性', () => {
  it('每个语义类别都有中文标签', () => {
    for (const s of Object.values(TOOL_SEMANTICS)) {
      expect(SEMANTIC_LABEL[s], `缺少 ${s} 的标签`).toBeTruthy()
    }
  })

  it('表的键都是小写（semanticOf 会先归一，键大写永远匹配不上）', () => {
    for (const k of Object.keys(TOOL_SEMANTICS)) {
      expect(k, `键 ${k} 必须小写`).toBe(k.toLowerCase())
    }
  })
})
