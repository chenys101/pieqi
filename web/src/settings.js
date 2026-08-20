// settings.js — 左侧任务栏底部「设置」区。
// 收起态是一条细栏（⚙ 设置），点击展开面板，内含：
//   - 渠道：飞书等渠道列表（点击/添加 → 打开配置模态框）
//   - 外网隧道：复用 tunnel.js 的控制面板
// 配置/扫码接口均为内网专用（BindOpGate），外网访问时渠道区显示提示。
import { authHeaders } from './auth.js';
import { mountTunnelPanel } from './tunnel.js';
import { mountLarkConfigModal, larkStatus } from './larkreg.js';

// 渠道注册表：当前仅支持飞书。未来新增渠道在此扩展，
// 「添加渠道」在多于一种类型时改为先弹类型选择。
const CHANNEL_TYPES = [
  { type: 'lark', name: '飞书', icon: '⚡', desc: '飞书 IM 渠道 — 扫码一键创建或手工配置' },
];

const API = '/api';

async function apiGet(path) {
  const r = await fetch(`${API}${path}`, { headers: authHeaders() });
  return { status: r.status, json: await r.json().catch(() => ({})) };
}

function escapeHtml(s) {
  return String(s).replace(/[&<>"']/g, c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
}

const mount = document.querySelector('#settings-mount');
if (mount) {
  mount.innerHTML = `
    <button id="settings-toggle" class="settings-toggle" aria-expanded="false" aria-controls="settings-panel">
      <span class="settings-label">⚙ 设置</span>
      <span class="settings-caret">▸</span>
    </button>
    <div id="settings-panel" class="settings-panel hidden">
      <div class="settings-group">
        <div class="settings-group-title">渠道</div>
        <div id="channel-list" class="channel-list"></div>
        <button id="channel-add" class="channel-add">＋ 添加渠道</button>
      </div>
      <div class="settings-group">
        <div class="settings-group-title">外网隧道</div>
        <div id="tunnel-panel"></div>
      </div>
    </div>`;

  // 展开/收起设置面板
  const toggle = document.getElementById('settings-toggle');
  const panel = document.getElementById('settings-panel');
  toggle.addEventListener('click', () => {
    const nowHidden = panel.classList.toggle('hidden');
    toggle.setAttribute('aria-expanded', String(!nowHidden));
    toggle.querySelector('.settings-caret').style.transform = nowHidden ? '' : 'rotate(90deg)';
  });

  // 打开渠道配置模态框（唯一类型时直开；多类型时先弹类型选择）
  function openChannelConfig(type) {
    const t = CHANNEL_TYPES.find(x => x.type === type);
    if (!t) return;
    const overlay = document.getElementById('config-overlay');
    mountLarkConfigModal(overlay, t);
  }
  document.getElementById('channel-add').addEventListener('click', () => {
    openChannelConfig(CHANNEL_TYPES[0].type);
  });

  // 渠道列表：拉接入状态渲染行 + 徽标
  const channelList = document.getElementById('channel-list');
  async function renderChannels() {
    const st = await apiGet('/larkreg/status');
    if (st.status === 403) {
      channelList.innerHTML = `<div class="channel-intranet-only">⚠ 仅内网可配置渠道</div>`;
      return;
    }
    const registered = st.status === 200 && st.json.registered;
    const appId = st.json.app_id || '';
    channelList.innerHTML = CHANNEL_TYPES.map(c => `
      <div class="channel-row" data-type="${c.type}" role="button" tabindex="0" title="${escapeHtml(c.desc)}">
        <span class="channel-icon">${c.icon}</span>
        <div class="channel-info">
          <div class="channel-name">${c.name}</div>
          <div class="channel-status ${registered ? 'ok' : ''}">${registered ? `已接入 · ${escapeHtml(appId)}` : '未接入'}</div>
        </div>
        <span class="channel-chevron">›</span>
      </div>`).join('');
    channelList.querySelectorAll('.channel-row').forEach(row => {
      const open = () => openChannelConfig(row.dataset.type);
      row.addEventListener('click', open);
      row.addEventListener('keydown', (e) => {
        if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); open(); }
      });
    });
  }
  renderChannels();
  // 配置保存/接入成功后刷新渠道行徽标
  document.addEventListener('pieqi:channel-changed', () => renderChannels());

  // 外网隧道（复用 tunnel.js；status 全员可读，控制仅 Lark 移动端）
  const tunnelSlot = document.getElementById('tunnel-panel');
  if (tunnelSlot) mountTunnelPanel(tunnelSlot);
}
