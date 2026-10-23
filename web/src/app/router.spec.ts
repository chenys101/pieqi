// 新建任务页的预选项目（`?project=`）—— 口径只有一条：**原样路径**。
//
// 为什么值得单独测（2026-10-24 的教训，一次修错了两轮）：
//   详情页那枚「在「x」新建任务」把当前任务的项目带过去，中间只有一次 URL 编解码
//   （浏览器 ↔ vue-router）。我一度以为"链接要自己 encodeURIComponent、页面再解一次"，
//   结果两边各编一次 ⇒ 落地的 project 变成 `D%253A%2F…`，新建页显示的是
//   `D:/code/go/pieqi/web`（"最近项目"兜底）而不是当前任务的 pieqi —— 而界面不报错，
//   看起来只是"预选没生效"。这条用例钉住"多解一次 / 多编一次"都不许发生。
import { describe, expect, it } from 'vitest'
import { newTaskProjectOf } from './router'

describe('newTaskProjectOf（?project= 取值口径）', () => {
  it('原样返回路径（不 trim 掉 Windows 路径里的反斜杠、不做 URL 解码）', () => {
    expect(newTaskProjectOf('D:\\code\\go\\pieqi')).toBe('D:\\code\\go\\pieqi')
    expect(newTaskProjectOf('D:/code/go/pieqi')).toBe('D:/code/go/pieqi')
    expect(newTaskProjectOf('/home/me/project')).toBe('/home/me/project')
    expect(newTaskProjectOf('\\\\server\\share\\proj')).toBe('\\\\server\\share\\proj')
  })

  it('百分号编码的形态**不再**二次解码（route.query 已经解过一次了）', () => {
    // 若这里被"再解一次"，`%253A` 会变成 `%3A`、`%41` 会变成 `A` —— 都是错的落点
    expect(newTaskProjectOf('D%253A%252Fcode')).toBe('D%253A%252Fcode')
    expect(newTaskProjectOf('C:\\work\\100%')).toBe('C:\\work\\100%')
  })

  it('缺参 / 非字符串 → 空串（调用方据此走"没有预选"的默认值逻辑）', () => {
    expect(newTaskProjectOf(undefined)).toBe('')
    expect(newTaskProjectOf(null)).toBe('')
    expect(newTaskProjectOf(['a', 'b'])).toBe('')
    expect(newTaskProjectOf('')).toBe('')
    expect(newTaskProjectOf('   ')).toBe('')
  })
})
