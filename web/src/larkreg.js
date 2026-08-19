// 飞书一键接入:扫码 → 轮询 → 凭据落盘 → 提示重启。
// 仅内网可调(后端 BindOpGateMiddleware 强制),所以这个入口只在
// 内网 PWA 上可见。外网访问时 start 会 403,前端按钮禁用。
//
// 复用 /api/tunnel/qrcode 端点把 QR URL 转成可扫描图片(避免
// 前端再引一个 QR 库)。
import { authHeaders } from './auth.js';

const POLL_INTERVAL_MS = 2000;
const POLL_TIMEOUT_MS = 10 * 60 * 1000; // 10 分钟,与后端 ctx 超时对齐

async function apiCall(method, path, body) {
  const opts = { method, headers: authHeaders() };
  if (body) opts.body = JSON.stringify(body);
  const r = await fetch(path, opts);
  return { status: r.status, json: await r.json().catch(() => ({})) };
}

async function startLarkReg(button) {
  button.disabled = true;
  const statusEl = document.querySelector('#larkreg-status');
  const qrEl = document.querySelector('#larkreg-qr');
  statusEl.textContent = '正在生成二维码...';
  qrEl.innerHTML = '';

  const start = await apiCall('POST', '/api/larkreg/start', {});
  if (start.status === 403) {
    statusEl.textContent = '⚠️ 仅内网可接入,请在内网环境操作';
    button.disabled = false;
    return;
  }
  if (start.status !== 200) {
    statusEl.textContent = `❌ 启动失败: ${start.json.error || start.status}`;
    button.disabled = false;
    return;
  }

  const qrUrl = start.json.qr_url;
  statusEl.textContent = '请在飞书里扫码确认';
  // 用 tunnel QR 端点把 URL 转成图片(避免前端引 QR 库)
  qrEl.innerHTML = `<img src="/api/tunnel/qrcode?text=${encodeURIComponent(qrUrl)}" alt="QR" width="256" height="256" />`;

  // 轮询
  const deadline = Date.now() + POLL_TIMEOUT_MS;
  const poll = async () => {
    if (Date.now() > deadline) {
      statusEl.textContent = '⏰ 超时,请重试';
      button.disabled = false;
      return;
    }
    const r = await apiCall('GET', '/api/larkreg/poll', null);
    if (r.status === 202) {
      statusEl.textContent = '等待扫码确认...';
      setTimeout(poll, POLL_INTERVAL_MS);
      return;
    }
    if (r.status === 200) {
      statusEl.textContent = `✅ 接入成功 (App ID: ${r.json.app_id})。请重启 Pieqi 生效。`;
      qrEl.innerHTML = '';
      button.disabled = false;
      return;
    }
    statusEl.textContent = `❌ ${r.json.error || r.status}`;
    button.disabled = false;
  };
  setTimeout(poll, POLL_INTERVAL_MS);
}

// 挂载按钮
const root = document.querySelector('#larkreg-mount');
if (root) {
  root.innerHTML = `
    <div class="larkreg-panel">
      <h3>接入飞书</h3>
      <p>扫码一键创建飞书自建应用,无需手动配置权限。</p>
      <button id="larkreg-btn" class="primary">扫码接入</button>
      <div id="larkreg-status"></div>
      <div id="larkreg-qr"></div>
    </div>
  `;
  root.querySelector('#larkreg-btn')?.addEventListener('click', (e) => startLarkReg(e.target));
}
