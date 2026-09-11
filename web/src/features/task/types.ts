// 任务浏览器页的视图模型（页面计算、手风琴组件渲染，两边必须同一个形状）。
//
// 单独成文件而不是各写一份 interface：**两份结构会漂移** ——
// 页面加了个字段、组件没加，TS 不会报错（结构兼容），但渲染会静默少一样东西。
import type { Task } from '@/types/task'

/** 一个项目在任务浏览器页里的全部呈现信息（已过滤、已排序） */
export interface BrowserGroup {
  key: string
  /** 项目名（project_id 或路径末段） */
  name: string
  /** 项目绝对路径（title 提示） */
  path: string
  tasks: Task[]
  open: boolean
  /** 筛选后无匹配任务：置灰且不可展开 */
  dim: boolean
  /** 状态聚合文案（"2 运行中 · 1 失败"） */
  agg: string
}
