// Tunnel control panel. Per PRD §6:
//   - PC browsers hide tunnel buttons entirely
//   - Lark/Feishu mobile shows the full panel
//   - status is shown for everyone (read-only)
import { authHeaders, isLarkMobile } from './auth.js';

const API = '/api';

async function apiCall(method, path, body) {
  const opts = { method, headers: authHeaders() };
  if (body) {
    opts.headers['Content-Type'] = 'application/json';
    opts.body = JSON.stringify(body);
  }
  const r = await fetch(`${API}${path}`, opts);
  const txt = await r.text();
  let j; try { j = JSON.parse(txt); } catch { j = { error: txt }; }
  if (!r.ok) throw new Error(j.error || `${path}: ${r.status}`);
  return j;
}

export async function mountTunnelPanel(root) {
  // Always render the status section; only render controls on Lark mobile.
  // 组标题「外网隧道」由 settings.js 的设置区提供，这里不再自带 h3。
  const canControl = isLarkMobile();
  root.innerHTML = `
    <div class="tunnel-panel">
      <div id="tunnel-status" class="tunnel-status">查询中…</div>
      ${canControl ? `
        <div class="tunnel-controls">
          <label>TTL
            <select id="tunnel-ttl">
              <option value="15m" selected>15 分钟</option>
              <option value="1h">1 小时</option>
              <option value="4h">4 小时</option>
            </select>
          </label>
          <button id="tunnel-start" class="primary">启动隧道</button>
          <button id="tunnel-stop" class="danger">关闭隧道</button>
          <button id="tunnel-reset">重置 Token</button>
          <button id="tunnel-renew">续期</button>
        </div>
      ` : `
        <div class="tunnel-controls hidden"></div>
      `}
      <div id="tunnel-result" class="tunnel-result"></div>
    </div>`;

  await refreshStatus(root);
  if (canControl) bindControls(root);
}

async function refreshStatus(root) {
  const slot = root.querySelector('#tunnel-status');
  if (!slot) return;
  try {
    const st = await apiCall('GET', '/tunnel/status');
    if (!st.active) {
      slot.textContent = '未运行';
      slot.classList.remove('active');
      return;
    }
    const exp = st.expires_at ? new Date(st.expires_at).toLocaleString() : '?';
    slot.innerHTML = `运行中 · <a href="${escapeAttr(st.tunnel_url)}" target="_blank">${escapeHtml(st.tunnel_url)}</a> · 到期 ${exp}`;
    slot.classList.add('active');
  } catch (e) {
    slot.textContent = `状态获取失败: ${e.message}`;
  }
}

function bindControls(root) {
  root.querySelector('#tunnel-start')?.addEventListener('click', async () => {
    const ttl = root.querySelector('#tunnel-ttl').value;
    const out = root.querySelector('#tunnel-result');
    out.textContent = '启动中…';
    try {
      const r = await apiCall('POST', '/tunnel/start', { ttl });
      // 刚启动的 quick tunnel 需要约 30~60 秒完成 DNS 注册/传播，期间直接
      // 访问会连接失败（cloudflared 自身提示 "may take some time to be reachable"）。
      renderTunnelResult(out, r, '⚠ 隧道刚启动，DNS 生效约需 30~60 秒。立即打开可能失败，请稍等片刻再访问。');
      await refreshStatus(root);
    } catch (e) {
      out.textContent = `启动失败: ${e.message}`;
    }
  });
  root.querySelector('#tunnel-stop')?.addEventListener('click', async () => {
    try {
      await apiCall('POST', '/tunnel/stop', {});
      root.querySelector('#tunnel-result').textContent = '已关闭';
      await refreshStatus(root);
    } catch (e) {
      alert(e.message);
    }
  });
  root.querySelector('#tunnel-reset')?.addEventListener('click', async () => {
    try {
      const r = await apiCall('POST', '/tunnel/reset', {});
      root.querySelector('#tunnel-result').innerHTML =
        `新 Token: <code>${escapeHtml(r.token)}</code>`;
    } catch (e) {
      alert(e.message);
    }
  });
  // 续期：token 值不变、过期时间延长，返回结构与启动隧道一致（链接/QR/token/到期时间）
  root.querySelector('#tunnel-renew')?.addEventListener('click', async () => {
    const ttl = root.querySelector('#tunnel-ttl').value;
    const out = root.querySelector('#tunnel-result');
    out.textContent = '续期中…';
    try {
      const r = await apiCall('POST', '/tunnel/renew', { ttl });
      renderTunnelResult(out, r, `已续期 +${ttl}，原链接与 Token 继续可用`);
      await refreshStatus(root);
    } catch (e) {
      out.textContent = `续期失败: ${e.message}`;
    }
  });
}

// renderTunnelResult 以与 /start 返回同构的方式渲染隧道结果：
// 可选 note 为顶部说明（续期专用），到期时间始终展示。
function renderTunnelResult(out, r, note = '') {
  out.innerHTML = `
    ${note ? `<div class="tunnel-note">${escapeHtml(note)}</div>` : ''}
    <div class="tunnel-link">
      <label>隧道链接（点击在飞书中打开）</label>
      <a href="${escapeAttr(r.lark_deep_link)}" target="_blank">${escapeHtml(r.lark_deep_link)}</a>
    </div>
    <div class="tunnel-qr" id="tunnel-qr"></div>
    <div class="tunnel-token">Token: <code>${escapeHtml(r.token)}</code> · 到期 ${escapeHtml(new Date(r.expires_at).toLocaleString())}</div>`;
  renderQR('tunnel-qr', r.lark_deep_link);
}

// renderQR uses the go-qrcode PNG endpoint exposed at /api/tunnel/qrcode?text=...
async function renderQR(slotId, text) {
  const slot = document.getElementById(slotId);
  if (!slot) return;
  const url = `/api/tunnel/qrcode?text=${encodeURIComponent(text)}`;
  slot.innerHTML = `<img src="${url}" alt="隧道二维码" />`;
}

function escapeHtml(s) {
  return String(s).replace(/[&<>"']/g, c => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));
}
function escapeAttr(s) { return escapeHtml(s); }
