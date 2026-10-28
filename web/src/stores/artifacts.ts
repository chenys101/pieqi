// Artifacts 共享 store：Timeline「本轮/全部产物」与 FeedbackPanel 行内打开入口的**同一份**清单。
//
// 事实源只有 `GET /api/tasks/:id/artifacts`（ADR-0018）。两处各拉一次就会出现
// 「面板有打开按钮、时间线还没有 / 条数对不上」——用户分不清是功能坏了还是还在加载。
import { defineStore } from 'pinia'
import { getTaskArtifacts } from '@/services/api/feedback'
import type { ArtifactDto } from '@/types/api'
import { artifactOpensInApp } from '@/features/feedback/filePreview'

export interface ArtifactViewing {
  taskId: string
  path: string
  kind: string
}

export const useArtifactsStore = defineStore('artifacts', {
  state: () => ({
    byTask: {} as Record<string, ArtifactDto[]>,
    inflight: {} as Record<string, Promise<void>>,
    /** 应用内只读 viewer 当前打开的文件（html 不进这里） */
    viewing: null as ArtifactViewing | null,
  }),

  getters: {
    list: (state) => (taskId: string): ArtifactDto[] => state.byTask[taskId] ?? [],
    byPath:
      (state) =>
      (taskId: string): Map<string, ArtifactDto> => {
        const map = new Map<string, ArtifactDto>()
        for (const a of state.byTask[taskId] ?? []) map.set(a.path, a)
        return map
      },
    forTurn:
      (state) =>
      (taskId: string, turn: number): ArtifactDto[] =>
        (state.byTask[taskId] ?? []).filter((a) => a.turn === turn),
  },

  actions: {
    /** force=false 时已有数据则跳过；force=true 无条件重拉（回退/新轮后） */
    async load(taskId: string, force = false): Promise<void> {
      if (!taskId) return
      if (!force && this.byTask[taskId]) return
      const running = this.inflight[taskId]
      if (running) return running
      const p = getTaskArtifacts(taskId)
        .then((list) => {
          this.byTask[taskId] = list
        })
        .catch(() => {
          // 与 feedbackBundle 同口径：失败不写空清单（避免"暂无产物"掩盖故障），也不上抛。
        })
        .finally(() => {
          delete this.inflight[taskId]
        })
      this.inflight[taskId] = p
      return p
    },

    /** html/image → 新标签；md/code/text/pdf → 应用内只读 sheet */
    openArtifact(taskId: string, a: ArtifactDto) {
      if (a.kind === 'html' || a.kind === 'image' || !artifactOpensInApp(a.kind)) {
        window.open(a.open, '_blank', 'noopener')
        return
      }
      this.viewing = { taskId, path: a.path, kind: a.kind }
    },

    openPath(taskId: string, path: string) {
      const a = this.byPath(taskId).get(path)
      if (a) {
        this.openArtifact(taskId, a)
        return
      }
      // 清单里没有时仍尝试只读打开（正文链到了未登记路径）
      this.viewing = { taskId, path, kind: '' }
    },

    closeViewer() {
      this.viewing = null
    },
  },
})
