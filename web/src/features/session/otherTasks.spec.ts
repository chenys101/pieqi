import { describe, expect, it } from 'vitest'
import type { Task, TaskStatus } from '@/types/task'
import { otherRunningTasks } from './otherTasks'

function task(id: string, status: TaskStatus, updatedAt = '2026-01-01T00:00:00Z'): Task {
  return { id, status, updatedAt, title: id, project: 'p' } as unknown as Task
}

describe('otherRunningTasks', () => {
  it('排除当前任务与终态，只留在途任务', () => {
    const tasks = [
      task('current', 'running'),
      task('done', 'completed'),
      task('failed', 'failed'),
      task('w', 'waiting_input'),
      task('r', 'running'),
      task('p', 'pending'),
    ]
    expect(otherRunningTasks(tasks, 'current').map((t) => t.id)).toEqual(['w', 'r', 'p'])
  })

  it('待决策优先，其次运行中，最后待运行（同档按 updatedAt 倒序）', () => {
    const tasks = [
      task('p1', 'pending', '2026-01-03T00:00:00Z'),
      task('r-old', 'running', '2026-01-01T00:00:00Z'),
      task('w-old', 'waiting_input', '2026-01-01T00:00:00Z'),
      task('r-new', 'running', '2026-01-02T00:00:00Z'),
      task('w-new', 'waiting_input', '2026-01-03T00:00:00Z'),
    ]
    expect(otherRunningTasks(tasks, 'none').map((t) => t.id)).toEqual(['w-new', 'w-old', 'r-new', 'r-old', 'p1'])
  })

  it('不修改入参，返回新数组', () => {
    const tasks = [task('b', 'running', '2026-01-01T00:00:00Z'), task('a', 'waiting_input', '2026-01-02T00:00:00Z')]
    const snapshot = tasks.map((t) => t.id)
    const out = otherRunningTasks(tasks, 'none')
    expect(tasks.map((t) => t.id)).toEqual(snapshot)
    expect(out).not.toBe(tasks)
  })
})
