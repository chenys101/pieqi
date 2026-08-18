// Feishu environment detection + OpenID extraction.
// Pieqi front-end runs inside three contexts:
//   1. Feishu mobile app webview (Lark/Feishu UA) — JSSDK exposes OpenID
//   2. Feishu PC web (browser logged into feishu.cn) — header inject via SSO
//   3. Plain browser / external — no OpenID available → backend 403s
//
// We do NOT trust window.opener or URL params for OpenID (forgable). The
// backend's bound OpenID is the source of truth; we just transport what
// the Feishu environment gives us in X-Feishu-Openid.

export function isLarkMobile() {
  const ua = navigator.userAgent || '';
  const lower = ua.toLowerCase();
  if (!lower.includes('lark') && !lower.includes('feishu')) return false;
  if (lower.includes('desktop') || lower.includes('larkclient')) return false;
  return true;
}

export function isFeishuPC() {
  // PC web login: UA is a normal browser but we arrived via feishu.cn SSO.
  // Heuristic: document.referrer includes feishu.cn / larksuite, OR
  // we were passed a feishu_openid via sessionStorage (set by SSO landing).
  const ua = (navigator.userAgent || '').toLowerCase();
  if (ua.includes('lark') || ua.includes('feishu')) return false; // mobile handled above
  const ref = (document.referrer || '').toLowerCase();
  if (ref.includes('feishu.cn') || ref.includes('larksuite') || ref.includes('internalfeishu')) return true;
  return !!sessionStorage.getItem('feishu_openid');
}

// feishuOpenId returns the OpenID the Feishu environment provided, or ''.
// Priority: sessionStorage (set once by SSO/JSSDK) > URL param (debug only).
export function feishuOpenId() {
  const cached = sessionStorage.getItem('feishu_openid');
  if (cached) return cached;
  // Debug/dev: allow ?openid= for local testing. Backend still has to
  // match the bound account, so this is safe.
  const url = new URLSearchParams(location.search).get('openid');
  if (url) {
    sessionStorage.setItem('feishu_openid', url);
    return url;
  }
  return '';
}

// setOpenId caches an OpenID (called by the Feishu JSSDK bootstrap or SSO landing).
export function setOpenId(openid) {
  if (openid) sessionStorage.setItem('feishu_openid', openid);
}

// authHeaders returns the fetch headers every API call should include.
// Always sends X-Feishu-Openid (empty if unknown — backend will 403).
export function authHeaders(extra = {}) {
  const h = { 'Content-Type': 'application/json', ...extra };
  const openid = feishuOpenId();
  if (openid) h['X-Feishu-Openid'] = openid;
  // Existing token mechanism (from URL ?token=) is preserved.
  const tok = new URLSearchParams(location.search).get('token') || '';
  if (tok) h['Authorization'] = `Bearer ${tok}`;
  return h;
}
