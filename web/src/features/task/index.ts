// Task Feature 出口
//
// 原 `TaskFilterBar`（状态 Tab + 项目过滤）已删除：它属于被取代的那版"任务列表页"，
// 其中的**项目下拉更是 SPEC §6.1 明令不做的**（"按空间筛选下拉不做 ——
// 列表主体已是全部项目，再筛同一维度就是第二次收窄，与树本身互相打架"）。
// 任务浏览器页的工具栏只有搜索 + 状态两项，写在该页内。
export { default as TaskBrowserGroup } from './components/TaskBrowserGroup.vue'
