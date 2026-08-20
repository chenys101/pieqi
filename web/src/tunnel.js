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
      out.innerHTML = `
        <div class="tunnel-link">
          <label>隧道链接（点击在飞书中打开）</label>
          <a href="${escapeAttr(r.lark_deep_link)}" target="_blank">${escapeHtml(r.lark_deep_link)}</a>
        </div>
        <div class="tunnel-qr" id="tunnel-qr"></div>
        <div class="tunnel-token">Token: <code>${escapeHtml(r.token)}</code></div>`;
      renderQR('tunnel-qr', r.lark_deep_link);
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
