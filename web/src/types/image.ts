// 待发图片的**协议形状**。
//
// 放在 types/ 而不是 features/session：它同时被新建任务页（features/task）、
// 会话 composable 与 session 组件使用。留在某个 feature 里会让别的域反向 import
// 那个 feature（web/README：features 之间不互相 import）。
//
// 注意这只是"浏览器内存里的待发对象"，不是后端协议：previewUrl 永不发给服务端。
export interface PendingImage {
  /** 本地唯一 id（列表 key 与删除用；不用文件名，重名会串） */
  id: string
  /** 预览用（data URL，仅存在于浏览器内存，绝不发给后端） */
  previewUrl: string
  /** 发送用：**纯 base64** */
  data: string
  /** MIME 类型 */
  mimeType: string
  /** 解码后字节数（展示与上限判定） */
  bytes: number
  /** 原始文件名（仅供展示） */
  name: string
}
