// fileGroups：文件清单按目录分组（默认收起）。
//
// 为什么分组：一个任务跑几轮后，累计变更动辄十几到几十个文件，平铺出来在手机上
// 就是一条瀑布 —— 用户要的通常不是"逐条看完"，而是**先看改了哪几个目录**，
// 再决定展开哪一个。分组头自带文件数与 +/- 合计，收起态就携带了
// "值不值得展开"的判据（与贴边条同理：收起不等于隐藏）。

export interface GroupableFile {
  /** repo 相对路径（git 口径，/ 分隔） */
  path: string
  additions?: number
  deletions?: number
}

export interface FileGroup<T extends GroupableFile> {
  /** 目录（repo 相对，根目录为 ''） */
  dir: string
  /** 分组头文案：目录路径，根目录显示为 '.' */
  label: string
  files: T[]
  additions: number
  deletions: number
}

/**
 * 根目录的显示名。
 * 不用 "." 或 "/"：在小屏上那是一个几乎看不见的点，既不像分组也不像可点的东西；
 * 分组头要回答的是"这一堆文件在哪个目录"，那就直接写出来。
 */
export const ROOT_GROUP_LABEL = '根目录'

/** 目录部分（'' = 根目录）。统一按 / 切，兼容 Windows 反斜杠路径。 */
export function dirOf(path: string): string {
  const p = path.replace(/\\/g, '/')
  const i = p.lastIndexOf('/')
  return i < 0 ? '' : p.slice(0, i)
}

/** 文件名（分组内只显示这一段，完整路径放 title） */
export function baseNameOf(path: string): string {
  const p = path.replace(/\\/g, '/')
  const i = p.lastIndexOf('/')
  return i < 0 ? p : p.slice(i + 1)
}

/**
 * 按目录分组。
 *
 * 顺序**不按出现顺序**：git 的 diff 顺序在改名 / 新增后会变，每次刷新列表跟着
 * 重排，用户就会找不到刚才那个目录。根目录固定在最前，其余按目录名升序。
 */
export function groupByFolder<T extends GroupableFile>(items: readonly T[]): FileGroup<T>[] {
  const map = new Map<string, FileGroup<T>>()
  for (const item of items) {
    const dir = dirOf(item.path)
    let g = map.get(dir)
    if (!g) {
      g = { dir, label: dir || ROOT_GROUP_LABEL, files: [], additions: 0, deletions: 0 }
      map.set(dir, g)
    }
    g.files.push(item)
    g.additions += item.additions ?? 0
    g.deletions += item.deletions ?? 0
  }
  return [...map.values()].sort((a, b) => {
    if (a.dir === b.dir) return 0
    if (a.dir === '') return -1
    if (b.dir === '') return 1
    return a.dir.localeCompare(b.dir)
  })
}
