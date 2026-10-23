// 工具结果图片：归一化过滤 + 时间线渲染。
//
// 守两条判据：
//  1. **只有 stored=true 才渲染**：历史事件里"记了元数据但没存字节"的图必须被滤掉，
//     否则页面上一排永远加载不出来的破图（看起来像图丢了，实际是从来没存过）。
//  2. 图落在 **tool_result** 上，且文字卡仍在（图是主体，信封是可核对的事实）。
import { describe, it, expect, beforeAll } from 'vitest'
import { createApp, h, nextTick, type Component } from 'vue'
import TimelineEventView from '@/features/timeline/components/TimelineEventView.vue'
import { normalizeEvents } from '@/services/websocket/normalizer'
import type { TaskEventDto } from '@/types/api'
import type { AgentEvent } from '@/types/event'

const HASH_A = 'a'.repeat(64)
const HASH_B = 'b'.repeat(64)

function dto(seq: number, extra: Partial<TaskEventDto>): TaskEventDto {
  return { seq, type: 'tool_result', at: new Date(0).toISOString(), ...extra } as TaskEventDto
}

function mount(component: Component, props: Record<string, unknown>) {
  const host = document.createElement('div')
  document.body.appendChild(host)
  const app = createApp({ render: () => h(component, props) })
  app.mount(host)
  return { host, app }
}

beforeAll(() => {
  // jsdom 没有 matchMedia；本组件不依赖它，但全局 fixture 里别处可能会调
  if (typeof window.matchMedia !== 'function') {
    window.matchMedia = (() => ({
      matches: false,
      addEventListener: () => {},
      removeEventListener: () => {},
    })) as unknown as typeof window.matchMedia
  }
})

describe('normalizeEvents：图片只在 stored=true 时透出', () => {
  it('滤掉未落盘的旧图，只留已落盘的', () => {
    const events = normalizeEvents('t1', [
      dto(1, {
        images: [
          { mime_type: 'image/png', bytes: 10, hash: HASH_A, stored: false }, // 旧数据：没存字节
          { mime_type: 'image/png', bytes: 20, hash: HASH_B, stored: true, name: 'A.png' },
        ],
      }),
    ])
    expect(events).toHaveLength(1)
    expect(events[0].payload.images).toHaveLength(1)
    expect(events[0].payload.images![0]).toMatchObject({ hash: HASH_B, name: 'A.png', bytes: 20 })
  })

  it('hash 形状不合法或缺失时整条丢弃（它要拼进 URL，不能带脏值）', () => {
    const events = normalizeEvents('t1', [
      dto(1, {
        images: [
          { mime_type: 'image/png', bytes: 1, stored: true }, // 没有 hash
          { mime_type: 'image/png', bytes: 1, hash: '../../etc', stored: true }, // 非法
        ],
      }),
    ])
    expect(events[0].payload.images).toBeUndefined()
  })

  it('没有图的事件不带 images 字段', () => {
    const events = normalizeEvents('t1', [dto(1, { result: 'plain text' })])
    expect(events[0].payload.images).toBeUndefined()
  })
})

describe('TimelineEventView：tool_result 的图片渲染', () => {
  function eventWith(images: AgentEvent['payload']['images']): AgentEvent {
    return {
      id: 't1:1',
      taskId: 't1',
      type: 'tool_result',
      timestamp: new Date(0).toISOString(),
      payload: { toolName: 'read_image', result: '<type>image</type>', images },
    }
  }

  it('有图时渲染缩略图，src 指向 attachments 端点', async () => {
    const { host, app } = mount(TimelineEventView, {
      event: eventWith([{ mimeType: 'image/png', bytes: 2048, hash: HASH_A, name: 'A.png' }]),
    })
    await nextTick()
    const img = host.querySelector('img')
    expect(img).not.toBeNull()
    expect(img!.getAttribute('src')).toBe(`/api/tasks/t1/attachments/${HASH_A}`)
    expect(img!.getAttribute('alt')).toBe('A.png')
    // 文字卡仍在（信封是可核对的事实）
    expect(host.textContent).toContain('read_image')
    app.unmount()
  })

  it('没有图时不渲染图片区（不摆空盒子）', async () => {
    const { host, app } = mount(TimelineEventView, { event: eventWith(undefined) })
    await nextTick()
    expect(host.querySelector('[data-testid="event-images"]')).toBeNull()
    app.unmount()
  })
})
