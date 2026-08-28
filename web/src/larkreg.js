// 飞书渠道配置：扫码一键创建 + 手动配置。
// 由 settings.js 的「设置」区触发，渲染到 #config-overlay 居中模态框
// （移动端自动降级为底部抽屉，见 styles.css）。
//
// 接口均为内网专用（后端 BindOpGateMiddleware）：外网（隧道）访问时
// status/config 返回 403，前端显示「仅内网」提示。
//
// 热应用：POST /api/larkreg/config 保存后后端直接生效（无需重启）；
// 仅接入方式 webhook↔longconn 切换时返回 restart_required=true。
import { authHeaders } from './auth.js';

const API = '/api';

const POLL_INTERVAL_MS = 2000;
const POLL_TIMEOUT_MS = 10 * 60 * 1000; // 10 分钟，与后端 ctx 超时对齐

async function apiCall(method, path, body) {
  const opts = { method, headers: authHeaders() };
  if (body) opts.body = JSON.stringify(body);
  const r = await fetch(`${API}${path}`, opts);
  return { status: r.status, json: await r.json().catch(() => ({})) };
}

// larkStatus 查询飞书接入状态（settings.js 渠道行徽标 + 模态框头部用）。
export async function larkStatus() {
  const st = await apiCall('GET', '/larkreg/status');
  return {
    status: st.status,
    ok: st.status === 200,
    registered: st.status === 200 && !!st.json.registered,
    appId: st.json.app_id || '',
  };
}

// mountLarkConfigModal 渲染配置模态框到 hostEl（#config-overlay）。
// channel 来自 settings.js 的渠道注册表。
export async function mountLarkConfigModal(hostEl, channel) {
  const st = await larkStatus();

  hostEl.innerHTML = `
    <div class="config-modal" role="dialog" aria-modal="true">
      <div class="config-head">
        <div>
          <h2>${channel.icon} ${channel.name}渠道配置</h2>
          <div class="config-sub">${st.registered
            ? `已接入 · <code>${escapeHtml(st.appId)}</code>`
            : '未接入 — 扫码一键创建或手动配置'}</div>
        </div>
        <button class="close" data-close-config aria-label="关闭">×</button>
      </div>
      <div class="config-tabs" role="tablist">
        <button class="config-tab active" data-tab="scan" role="tab">扫码一键创建</button>
        <button class="config-tab" data-tab="manual" role="tab">手动配置</button>
      </div>
      <div class="config-body">
        <div id="config-tab-scan" class="config-tab-pane" role="tabpanel"></div>
        <div id="config-tab-manual" class="config-tab-pane hidden" role="tabpanel"></div>
      </div>
      <div id="config-result" class="config-result"></div>
    </div>`;
  hostEl.classList.remove('hidden');
  hostEl.setAttribute('aria-hidden', 'false');

  // 关闭：× 按钮 / 点遮罩 / ESC
  const close = () => {
    hostEl.classList.add('hidden');
    hostEl.setAttribute('aria-hidden', 'true');
    hostEl.innerHTML = '';
    document.removeEventListener('keydown', onKey);
  };
  const onKey = (e) => { if (e.key === 'Escape') close(); };
  document.addEventListener('keydown', onKey);
  hostEl.querySelector('[data-close-config]').addEventListener('click', close);
  hostEl.addEventListener('click', (e) => { if (e.target === hostEl) close(); });

  // Tab 切换
  hostEl.querySelectorAll('.config-tab').forEach(tab => {
    tab.addEventListener('click', () => {
      hostEl.querySelectorAll('.config-tab').forEach(x => x.classList.toggle('active', x === tab));
      hostEl.querySelector('#config-tab-scan').classList.toggle('hidden', tab.dataset.tab !== 'scan');
      hostEl.querySelector('#config-tab-manual').classList.toggle('hidden', tab.dataset.tab !== 'manual');
    });
  });

  mountScanTab(hostEl.querySelector('#config-tab-scan'), hostEl);
  mountManualTab(hostEl.querySelector('#config-tab-manual'), hostEl);
}

// --- 扫码一键创建 ---

function mountScanTab(container, hostEl) {
  container.innerHTML = `
    <p class="config-hint">扫码一键创建飞书自建应用，无需手动配置权限。仅内网可用。</p>
    <button id="scan-start" class="primary">扫码接入</button>
    <div id="scan-status" class="scan-status"></div>
    <div id="scan-qr" class="scan-qr"></div>`;

  container.querySelector('#scan-start').addEventListener('click', async (e) => {
    const btn = e.target;
    btn.disabled = true;
    const statusEl = container.querySelector('#scan-status');
    const qrEl = container.querySelector('#scan-qr');
    statusEl.textContent = '正在生成二维码...';
    qrEl.innerHTML = '';

    const start = await apiCall('POST', '/larkreg/start', {});
    if (start.status === 403) {
      statusEl.textContent = '⚠️ 仅内网可接入，请在内网环境操作';
      btn.disabled = false;
      return;
    }
    if (start.status !== 200) {
      statusEl.textContent = `❌ 启动失败: ${start.json.error || start.status}`;
      btn.disabled = false;
      return;
    }

    const qrUrl = start.json.qr_url;
    statusEl.textContent = '请在飞书里扫码确认';
    // 复用 /api/tunnel/qrcode 端点把 QR URL 转成可扫描图片（避免再引 QR 库）
    qrEl.innerHTML = `<img src="/api/tunnel/qrcode?text=${encodeURIComponent(qrUrl)}" alt="QR" />`;

    // 轮询结果（成功后后端已热应用，按 hint 提示）
    const deadline = Date.now() + POLL_TIMEOUT_MS;
    const poll = async () => {
      if (Date.now() > deadline) {
        statusEl.textContent = '⏰ 超时，请重试';
        btn.disabled = false;
        return;
      }
      const r = await apiCall('GET', '/larkreg/poll', null);
      if (r.status === 202) {
        statusEl.textContent = '等待扫码确认...';
        setTimeout(poll, POLL_INTERVAL_MS);
        return;
      }
      if (r.status === 200) {
        statusEl.textContent = `✅ 接入成功 (App ID: ${r.json.app_id})${r.json.hint ? ' · ' + r.json.hint : ''}`;
        qrEl.innerHTML = '';
        btn.disabled = false;
        return;
      }
      statusEl.textContent = `❌ ${r.json.error || r.status}`;
      btn.disabled = false;
    };
    setTimeout(poll, POLL_INTERVAL_MS);
  });
}

// --- 手动配置 ---

function mountManualTab(container, hostEl) {
  // 预填 app_id/event_mode；secret 等不回显（secret_set=false 时 secret 必填，
  // 否则留空保持原值）。
  apiCall('GET', '/larkreg/config').then((r) => {
    const appId = (r.json && r.json.app_id) || '';
    const eventMode = (r.json && r.json.event_mode) || 'longconn';
    const secretSet = !!(r.json && r.json.secret_set);
    container.innerHTML = `
      <form id="manual-form" class="config-form">
        <label>接入方式
          <select id="manual-mode">
            <option value="longconn" ${eventMode === 'longconn' ? 'selected' : ''}>长连接 longconn（推荐，无需公网回调）</option>
            <option value="webhook" ${eventMode === 'webhook' ? 'selected' : ''}>Webhook（需公网回调地址）</option>
          </select>
        </label>
        <label>App ID <input id="manual-appid" value="${escapeAttr(appId)}" placeholder="cli_xxxxxxxx" required autocomplete="off" /></label>
        <label>App Secret <input id="manual-secret" type="password" placeholder="${secretSet ? '留空则保持原值' : '必填'}" ${secretSet ? '' : 'required'} autocomplete="new-password" /></label>
        <div id="manual-webhook-fields" class="hidden">
          <label>Verify Token <input id="manual-vt" placeholder="留空则保持原值" autocomplete="off" /></label>
          <label>Encrypt Key <input id="manual-ek" placeholder="留空则保持原值" autocomplete="off" /></label>
        </div>
        <div class="config-actions">
          <button type="submit" class="primary" id="manual-save">保存并应用</button>
        </div>
      </form>`;

    const mode = container.querySelector('#manual-mode');
    const webhookFields = container.querySelector('#manual-webhook-fields');
    const syncWebhook = () => webhookFields.classList.toggle('hidden', mode.value !== 'webhook');
    mode.addEventListener('change', syncWebhook);
    syncWebhook();

    container.querySelector('#manual-form').addEventListener('submit', async (e) => {
      e.preventDefault();
      const body = {
        app_id: container.querySelector('#manual-appid').value.trim(),
        app_secret: container.querySelector('#manual-secret').value.trim(),
        verify_token: container.querySelector('#manual-vt').value.trim(),
        encrypt_key: container.querySelector('#manual-ek').value.trim(),
        event_mode: mode.value,
      };
      const saveBtn = container.querySelector('#manual-save');
      const resultEl = hostEl.querySelector('#config-result');
      saveBtn.disabled = true;
      saveBtn.textContent = '保存中...';
      resultEl.textContent = '';
      const r = await apiCall('POST', '/larkreg/config', body);
      saveBtn.disabled = false;
      saveBtn.textContent = '保存并应用';
      if (r.status === 200) {
        resultEl.className = 'config-result ok';
        resultEl.textContent = `✅ ${r.json.message || '已保存'}`;
        container.querySelector('#manual-secret').value = ''; // 清空，避免下次误提交已配置 secret
        // 通知渠道行刷新状态
        document.dispatchEvent(new CustomEvent('pieqi:channel-changed', { detail: { type: 'lark' } }));
      } else if (r.status === 403) {
        resultEl.className = 'config-result';
        resultEl.textContent = '⚠️ 仅内网可配置渠道';
      } else {
        resultEl.className = 'config-result err';
        resultEl.textContent = `❌ ${r.json.error || r.status}`;
      }
    });
  });
}

function escapeHtml(s) {
  return String(s).replace(/[&<>"']/g, c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
}
function escapeAttr(s) { return escapeHtml(s); }
