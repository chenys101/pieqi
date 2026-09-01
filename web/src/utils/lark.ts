// 飞书环境检测（迁移自 V1 auth.js）：
// 1. 飞书移动 webview（Lark/Feishu UA）
// 2. 飞书 PC 网页（feishu.cn SSO 落地）
// 3. 普通浏览器 / 外网隧道

export function isLarkMobile(): boolean {
  const ua = navigator.userAgent || ''
  const lower = ua.toLowerCase()
  if (!lower.includes('lark') && !lower.includes('feishu')) return false
  if (lower.includes('desktop') || lower.includes('larkclient')) return false
  return true
}

export function isFeishuPC(): boolean {
  const ua = (navigator.userAgent || '').toLowerCase()
  if (ua.includes('lark') || ua.includes('feishu')) return false
  const ref = (document.referrer || '').toLowerCase()
  if (ref.includes('feishu.cn') || ref.includes('larksuite') || ref.includes('internalfeishu')) return true
  return !!sessionStorage.getItem('feishu_openid')
}
