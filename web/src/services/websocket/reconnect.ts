// 指数退避重连策略（方案 §12）：1s → 2s → 4s → 8s → 16s → 30s（封顶）
// 网络恢复后由首次成功连接 reset 回 1s。

const STEPS_MS = [1000, 2000, 4000, 8000, 16000, 30000]
const MAX_MS = 30000

export class ReconnectPolicy {
  private attempt = 0

  /** 下一次重连等待时间（毫秒） */
  next(): number {
    const delay = STEPS_MS[Math.min(this.attempt, STEPS_MS.length - 1)] ?? MAX_MS
    this.attempt++
    return delay
  }

  /** 连接成功后重置退避 */
  reset(): void {
    this.attempt = 0
  }

  /** 当前是第几次重试（UI 提示用） */
  get attempts(): number {
    return this.attempt
  }
}
