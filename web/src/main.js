import './styles.css';
import { attachAutocomplete } from './autocomplete.js';
import { authHeaders, tunnelToken } from './auth.js';

const API = '/api';
let token = tunnelToken(); // 持久化隧道 token：首次从 URL 捕获，刷新/桌面启动丢失 query 也能兜底
function headers() {
  // Delegate to auth.js so X-Feishu-Openid is always sent.
  return authHeaders();
}

// --- 鉴权问题提示 ---
// 外网请求被 401 拒绝（token 缺失/过期/被新隧道作废）时，给出明确的一次性提示，
// 避免静默空页面。内网访问永远放行，不会误触发。
let authIssueShown = false;
function notifyAuthIssue() {
  if (authIssueShown) return;
  authIssueShown = true;
  const banner = document.getElementById('debug-banner');
  if (banner) {
    banner.textContent = '⚠ 访问被拒绝：隧道 token 无效或已过期。请重新获取带 ?token= 的完整链接打开。';
    banner.classList.remove('hidden');
  }
}

const state = {
  tasks: [],          // 扁平任务列表
  expanded: new Set(), // 展开的 project_path（默认全收起；running/waiting_input 自动展开）
  selectedId: null,   // 当前选中任务 id
  hoverProjectPath: null, // 鼠标悬停的项目路径，新建任务时预选
  newTaskDefaultPath: null, // 进入新建态时预置的项目路径（默认取详情页当前任务的 project_path）
  ws: null,
  pendingScroll: null, // 发送补充后标记：收到下次任务更新强制滑到底
  thinking: {},       // taskId -> bool: 本次 run 尚未产出流式文本（展示「思考中...」徽章）
  // 防重复提交：
  creatingTask: false,         // 新建会话在途：请求未返回前忽略再次点击/回车
  submittingTasks: new Set(),  // 追加提示在途：taskId 集合，请求未返回前拦截重复提交
};

// 补全数据源：启动时拉取
const acSources = { commands: [], skills: [] };

// --- API ---
async function apiGet(path) {
  const r = await fetch(`${API}${path}`, { headers: headers() });
  if (r.status === 401) notifyAuthIssue();
  if (!r.ok) throw new Error(`${path}: ${r.status}`);
  return r.json();
}
async function apiPost(path, body) {
  const r = await fetch(`${API}${path}`, { method: 'POST', headers: headers(), body: JSON.stringify(body) });
  if (r.status === 401) notifyAuthIssue();
  const txt = await r.text();
  let j; try { j = JSON.parse(txt); } catch { j = { error: txt }; }
  if (!r.ok) throw new Error(j.error || `${path}: ${r.status}`);
  return j;
}
async function apiDelete(path) {
  const r = await fetch(`${API}${path}`, { method: 'DELETE', headers: headers() });
  if (r.status === 401) notifyAuthIssue();
  if (!r.ok) {
    let msg = `${path}: ${r.status}`;
    try { const j = await r.json(); if (j.error) msg = j.error; } catch {}
    throw new Error(msg);
  }
  return r.text();
}

// --- 渲染：侧栏任务列表 ---

// 分组 key：统一分隔符并转小写（Windows 路径大小写不敏感）。
// 同一项目可能因历史数据混用 "/" 与 "\"（如 G:/... vs G:\...）而呈现不同字符串，
// 归一后归为同一分组，避免侧栏出现重复分组（如 pieqi 出现两次）。
function groupKey(p) {
  return String(p || '').replace(/\\/g, '/').replace(/\/{2,}/g, '/').replace(/\/+$/, '').toLowerCase();
}

function groupByProject(tasks) {
  const m = new Map();
  for (const t of tasks) {
    const key = groupKey(t.project_path);
    if (!m.has(key)) m.set(key, { project_id: t.project_id, project_path: t.project_path, tasks: [] });
    m.get(key).tasks.push(t);
  }
  // 每组内任务按最新对话时间（updated_at）倒序：最近活跃的排最前
  for (const g of m.values()) {
    g.tasks.sort((a, b) => ts(b) - ts(a));
  }
  return [...m.values()];
}

// 取任务最新对话时间戳，缺省回退到创建时间；解析失败视为 0
function ts(t) {
  const v = new Date(t.updated_at || t.created_at || 0).getTime();
  return isNaN(v) ? 0 : v;
}

function statusLabel(s) {
  return ({ pending: '待运行', running: '运行中', waiting_input: '需决策', completed: '完成', failed: '失败', cancelled: '已取消' })[s] || s;
}

function timeAgo(iso) {
  if (!iso) return '';
  const s = Math.floor((Date.now() - new Date(iso)) / 1000);
  if (s < 60) return `${s}s`;
  if (s < 3600) return `${Math.floor(s/60)}m`;
  if (s < 86400) return `${Math.floor(s/3600)}h`;
  return `${Math.floor(s/86400)}d`;
}

function counts(tasks) {
  const c = {};
  for (const t of tasks) c[t.status] = (c[t.status] || 0) + 1;
  return c;
}

function render() {
  const root = document.getElementById('projects');
  const empty = document.getElementById('empty');
  const groups = groupByProject(state.tasks);
  if (groups.length === 0) {
    root.innerHTML = '';
    empty.classList.remove('hidden');
    return;
  }
  empty.classList.add('hidden');
  root.innerHTML = groups.map(g => {
    const c = counts(g.tasks);
    // 默认收起；但含 running/waiting_input（需关注）的分组自动展开，避免漏看在跑的任务
    const hasActive = (c.running || 0) > 0 || (c.waiting_input || 0) > 0;
    const collapsed = hasActive ? false : !state.expanded.has(g.project_path);
    const warn = (c.waiting_input || 0) > 0;
    return `
    <section class="project ${collapsed ? 'collapsed' : ''}" data-path="${g.project_path}" data-project-id="${g.project_id || g.project_path}">
      <header class="project-header">
        <span class="caret">▸</span>
        <span class="project-name">${g.project_id || g.project_path}</span>
        <span class="counts ${warn ? 'warn' : ''}">
          ${c.running ? `<span><b>${c.running}</b>运行</span>` : ''}
          ${c.waiting_input ? `<span>⚠<b>${c.waiting_input}</b></span>` : ''}
          ${c.completed ? `<span><b>${c.completed}</b>完成</span>` : ''}
        </span>
      </header>
      <ul class="task-list">${g.tasks.map(t => renderTaskItem(t)).join('')}</ul>
    </section>`;
  }).join('');

  root.querySelectorAll('.project-header').forEach(h => {
    h.addEventListener('click', () => {
      const path = h.closest('.project').dataset.path;
      if (state.expanded.has(path)) state.expanded.delete(path);
      else state.expanded.add(path);
      render();
    });
    // 悬停预选：鼠标在某项目组上时，新建任务默认用该项目
    const pid = h.closest('.project').dataset.projectId;
    const ppath = h.closest('.project').dataset.path;
    h.parentElement.addEventListener('mouseenter', () => { state.hoverProjectPath = ppath || pid; });
  });
  root.querySelectorAll('.task').forEach(el => {
    el.addEventListener('click', (e) => {
      // 点「更多操作」按钮不触发选中，交给按钮自己的 handler
      if (e.target.closest('[data-action="more"]')) return;
      selectTask(el.dataset.id);
    });
  });
  root.querySelectorAll('[data-action="more"]').forEach(btn => {
    btn.addEventListener('click', (e) => {
      e.stopPropagation();
      openTaskMenu(btn, btn.dataset.id);
    });
  });
}

function renderTaskItem(t) {
  const isFailed = t.status === 'failed';
  return `
  <li class="task ${t.status} ${t.id === state.selectedId ? 'selected' : ''}" data-id="${t.id}">
    <div class="task-row">
      <span class="status-mark ${isFailed ? 'failed' : ''}" title="${isFailed ? '执行失败' : ''}"></span>
      <span class="task-prompt-text" title="${escapeHtml(t.prompt || '')}">${escapeHtml(taskTitle(t))}</span>
      <span class="task-ago">${timeAgo(t.updated_at || t.created_at)}</span>
      <button class="task-more" data-action="more" data-id="${t.id}" title="更多操作" aria-label="更多操作">⋯</button>
    </div>
  </li>`;
}

function escapeHtml(s) {
  return String(s).replace(/[&<>"']/g, c => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));
}

// taskTitle 任务标题：优先用大模型异步生成的一句话标题；未生成时退回 prompt 智能截断。
function taskTitle(t) {
  return (t && t.title) || titleText(t && t.prompt);
}

// titleText 任务标题：智能截断。优先收成首个断句短标题；否则在词边界截断，
// 避免在句子中间硬切。`title` 属性仍展示完整 prompt（悬停可见全文）。
// 大模型异步生成一句话标题（task.title）后由 taskTitle 优先采用，本函数仅作兜底。
function titleText(s) {
  s = String(s || '').replace(/\s+/g, ' ').trim();
  const MAX = 15;
  if (s.length <= MAX) return s;
  // 1) 取首个断句（。！？.!?；;）前的完整短句作为标题（略超 MAX 也可接受）
  const m = s.match(/^[^。！？.!?；;]*[。！？.!?；;]/);
  if (m && m[0].trim().length <= MAX + 6) return m[0].trim() + '…';
  // 2) 否则在 MAX 内最近的词边界截断；无空格（如中文长句）则回到 MAX 硬切
  const sp = s.lastIndexOf(' ', MAX);
  return s.slice(0, sp >= 1 ? sp : MAX) + '…';
}

// --- 渲染：详情面板 ---
// URL 路由：当前选中会话对应 /<base>/session/<taskId>；新建态（无选中）为 /<base>/。
// 支持深链接 / 刷新保持选中 / 浏览器前进后退。应用可能挂在子路径（如隧道），
// 用 /session/ 前的部分作为 base 前缀。
const sessionPrefix = (() => {
  const m = location.pathname.match(/^(.*?)\/session\/[^/]+$/);
  return (m ? m[1] : location.pathname).replace(/\/+$/, '');
})();
function sessionIdFromUrl() {
  const m = location.pathname.match(/\/session\/([^/]+)/);
  return m ? decodeURIComponent(m[1]) : null;
}
// 同步 URL 到当前选中态：有选中 → pushState 到 /session/<id>；无选中（新建态）→ replaceState 回 base。
function syncUrl() {
  const target = state.selectedId ? `${sessionPrefix}/session/${state.selectedId}` : `${sessionPrefix}/`;
  if (location.pathname !== target) {
    if (state.selectedId) history.pushState(null, '', target);
    else history.replaceState(null, '', target);
  }
}
// 任务列表就绪后，从 URL 恢复选中的会话；URL 指向的会话不存在则回落到新建态。
function applyUrlSelection() {
  const sid = sessionIdFromUrl();
  if (sid && state.tasks.some(t => t.id === sid)) {
    selectTask(sid);
  } else if (sid) {
    syncUrl(); // URL 指向已删除/不存在的会话 → 复位到 base
  }
}

function selectTask(id, opts = {}) {
  state.selectedId = id;
  state.pendingScroll = id; // 切换任务时跳到最新对话
  // 选中任务时确保其所在分组展开（默认收起场景下，选中即聚焦）
  const t = state.tasks.find(x => x.id === id);
  if (t && t.project_path) state.expanded.add(t.project_path);
  render(); // 更新侧栏选中态 + 展开分组
  scrollTaskIntoView(id); // 侧栏滚动定位到关联任务
  renderDetail();
  closeSidebar(); // 移动端：点任务后自动收起任务栏，只展示详情页
  if (!opts.noUrl) syncUrl(); // 选中 → URL 变 /session/<id>（popstate 恢复时跳过）
}

// 选中任务后滚动侧栏，把关联任务定位到可见区（URL 深链接/刷新/点击进入时）。
// 仅当任务超出可视区时滚动使其进入，贴近顶部留 8px 边距。
function scrollTaskIntoView(id) {
  if (isMobile()) return; // 移动端侧栏抽屉选中即收起，无需定位
  const el = document.querySelector(`li.task[data-id="${id}"]`);
  const scroller = document.querySelector('#sidebar');
  if (!el || !scroller) return;
  const cr = scroller.getBoundingClientRect();
  const er = el.getBoundingClientRect();
  if (er.top < cr.top || er.bottom > cr.bottom) {
    scroller.scrollTop += er.top - cr.top - 8;
  }
}

function renderDetail() {
  const wrap = document.getElementById('detail-content');
  let t = state.selectedId ? state.tasks.find(x => x.id === state.selectedId) : null;
  if (state.selectedId && !t) {
    state.selectedId = null;
    t = null;
  }

  // ===== 新建态：与查看态共用同一页面结构（header + 滚动区 + 底部输入框 footer），
  // 仅 header 为「新建任务」、footer 内含项目选择。输入框统一在最底部。 =====
  if (!t) {
    wrap.classList.remove('hidden');
    wrap.innerHTML = `
      <div class="detail-content">
        <div class="detail-header">
          <div class="new-task-title">新建任务</div>
        </div>
        <div id="events" class="events"></div>
      </div>
      <div class="detail-footer">
        <div class="nt-project-row">
          <div class="project-picker">
            <select id="nt-project"></select>
            <button type="button" id="nt-custom-toggle" class="custom-toggle">自定义路径</button>
          </div>
          <input id="nt-project-path" class="hidden" type="text" placeholder="输入绝对路径，如 G:\workspace\erp" />
        </div>
        <div class="iv-input-wrap">
          <textarea id="nt-prompt" rows="3" placeholder="描述要做什么... 输入 / 触发命令/skill"></textarea>
          <button id="nt-send" class="send-btn" disabled title="创建任务 (Ctrl+Enter)" aria-label="创建任务">
            <svg viewBox="0 0 24 24" aria-hidden="true"><path d="M12 19V5M5 12l7-7 7 7"/></svg>
          </button>
          <div id="nt-autocomplete" class="autocomplete hidden"></div>
        </div>
      </div>`;
    populateProjectPicker();
    bindNewTaskFooter();
    return;
  }

  wrap.classList.remove('hidden');

  const idShort = (t.id || '').slice(0, 8);
  const isFinished = t.status === 'completed' || t.status === 'failed' || t.status === 'cancelled';
  const isRunning = t.status === 'running' || t.status === 'waiting_input' || t.status === 'pending';
  const canCancel = isRunning;

  // 重建前记录旧滚动容器状态：重建会用新元素替换 DOM、scrollTop 归零，
  // 必须在此刻判断「用户是否本就在底部（需跟随最新）」「刚发送补充需强制到底」
  // 「还是在中部翻看历史（重建后需恢复原位置）」，重建后再据此恢复滚动，
  // 否则长内容下 view 会被重置到最开头。
  const prevScroll = (() => {
    const s = document.querySelector('.detail-content');
    if (!s) return { nearBottom: true, force: state.pendingScroll === t.id, scrollTop: null };
    return {
      nearBottom: s.scrollHeight - s.scrollTop - s.clientHeight < 120,
      force: state.pendingScroll === t.id,
      scrollTop: s.scrollTop,
    };
  })();
  let dec = '';
  if (t.current_decision) {
    const cd = t.current_decision;
    if (cd.kind === 'choice') {
      // 路径 B 已禁用（改为纯文本方案拍板协议），正常不再触发。
      // 保留兜底：老任务若残留 choice waiting_input，提示用户直接回复选择。
      dec = `<div class="ev ev-choice">
        <div class="ev-head"><span class="icon">❓</span>${escapeHtml(cd.summary || '请选择')}</div>
        <div class="ev-body">模型已列出方案，请在下方输入框直接回复你的选择。</div>
      </div>`;
    } else {
      // 路径 A：approve/deny
      dec = `<div class="ev ev-tool-use">
        <div class="ev-head"><span class="icon">⚠</span>需决策：${escapeHtml(cd.tool_name)} - ${escapeHtml(cd.summary)}</div>
        <div class="detail-actions">
          <button class="primary" data-action="approve">✓ 批准</button>
          <button class="danger" data-action="deny">✗ 拒绝</button>
        </div>
      </div>`;
    }
  }

  // 底部操作区：始终显示输入框 + 右下角双态按钮
  // - running/pending/waiting_input：按钮翻转为方形「中止」（■），点击终止生成
  // - completed/failed/cancelled：按钮为「发送」（↑），有内容才可用（Ctrl+Enter）
  let footer = `<div class="detail-footer">
    <div class="iv-input-wrap">
      <textarea id="supplement-input" rows="3" placeholder="发送补充… 输入 / 触发命令/skill，Ctrl+Enter 发送"${canCancel ? ' disabled' : ''}></textarea>
      <button id="supplement-send" class="send-btn${canCancel ? ' stop' : ''}" ${canCancel ? '' : 'disabled'} title="${canCancel ? '中止生成' : '发送 (Ctrl+Enter)'}" aria-label="${canCancel ? '中止' : '发送'}">
        ${canCancel
          ? '<svg viewBox="0 0 24 24" aria-hidden="true"><rect x="6" y="6" width="12" height="12" rx="2"/></svg>'
          : '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M12 19V5M5 12l7-7 7 7"/></svg>'}
      </button>
      <div id="supplement-autocomplete" class="autocomplete hidden"></div>
    </div>
  </div>`;

  wrap.innerHTML = `
    <div class="detail-content">
      <div class="detail-header">
        <div class="detail-meta">
          <span class="badge">${statusLabel(t.status)}</span>
          <span class="task-id">#${idShort}</span>
          <span>${t.project_id || ''}</span>
          <span>${timeAgo(t.updated_at || t.created_at)}前</span>
        </div>
        <div class="prompt" title="${escapeHtml(t.prompt || '')}">${escapeHtml(taskTitle(t))}</div>
        ${dec}
      </div>
      <div id="events" class="events"></div>
    </div>
    ${footer}`;

  renderEvents(t);
  // 滚动恢复（基于重建前的 prevScroll）：
  //  - 刚发送补充（force）→ 强制到底，一次性并清标记；
  //  - 任务仍在推进且用户原本在底部 → 跟随最新输出；
  //  - 用户在中部翻看历史（非底部、非 force）→ 恢复原位置，避免被重置到开头。
  if (prevScroll.force || (prevScroll.nearBottom && isRunning)) {
    const scroller = document.querySelector('.detail-content');
    if (scroller) scroller.scrollTop = scroller.scrollHeight;
    if (prevScroll.force) state.pendingScroll = null;
  } else if (prevScroll.scrollTop != null) {
    const scroller = document.querySelector('.detail-content');
    if (scroller) scroller.scrollTop = prevScroll.scrollTop;
  }
  wrap.querySelectorAll('[data-action]').forEach(b => {
    b.addEventListener('click', () => onTaskAction(b.dataset.action, t.id, b.dataset.option));
  });
  // 输入框 + 右下角双态按钮（中止 ■ / 发送 ↑）
  const supInput = document.getElementById('supplement-input');
  const supSend = document.getElementById('supplement-send');
  const supAc = document.getElementById('supplement-autocomplete');
  if (supInput && supSend) {
    if (canCancel) {
      // 中止态：■ 方块，点击立即终止当前生成
      supSend.disabled = false;
      supSend.addEventListener('click', () => onTaskAction('cancel', t.id));
    } else {
      // 发送态：↑ 箭头，有内容才可用 + Ctrl+Enter
      supInput.addEventListener('input', () => { supSend.disabled = !supInput.value.trim(); });
      supInput.addEventListener('keydown', (e) => {
        if ((e.ctrlKey || e.metaKey) && e.key === 'Enter' && !supSend.disabled) {
          e.preventDefault();
          supSend.click();
        }
      });
      supSend.addEventListener('click', async () => {
        if (state.submittingTasks.has(t.id)) return; // 在途防重：请求未返回前忽略重复点击/回车
        const text = supInput.value.trim();
        if (!text) return;
        // 立即置在途标记 + 禁用按钮，覆盖整个 await 窗口（此前连点会重复发 intervene）
        state.submittingTasks.add(t.id);
        supSend.disabled = true;
        try {
          await apiPost(`/tasks/${t.id}/intervene`, { kind: 'append_prompt', text });
          supInput.value = '';
          supInput.disabled = true; // 提交后清空并禁止输入，等待任务状态刷新重建 footer
          supSend.disabled = true;
          state.thinking[t.id] = true; // 进入「思考中...」占位态，首次流式文本后清除
          state.pendingScroll = t.id; // 标记：收到下次更新后强制滑到底
          // 乐观渲染：发送成功后本地立即在详情区插入右对齐用户气泡 + 「思考中...」徽章，
          // 不等 WS 推送。后端 Resume 会持久化同一批事件并经 task_updated 推回，
          // 届时 renderDetail 全量重绘会替换这些临时节点，故不写 state.tasks.events。
          if (state.selectedId === t.id) {
            const eventsEl = document.getElementById('events');
            if (eventsEl) {
              eventsEl.insertAdjacentHTML('beforeend',
                `<div class="ev ev-user"><div class="ev-bubble">${escapeHtml(text)}</div></div>` +
                thinkingBadgeHTML());
              const scroller = document.querySelector('.detail-content');
              if (scroller) scroller.scrollTop = scroller.scrollHeight;
            }
          }
        } catch (err) {
          supSend.disabled = false; // 失败恢复可提交，允许重试
          alert(err.message);
        } finally {
          state.submittingTasks.delete(t.id);
        }
      });
      // 斜杠补全（commands + skills 分组）
      if (supAc) attachAutocomplete(supInput, supAc, acSources);
    }
  }
}

// --- 事件流渲染 ---
// 注意：滚动位置的恢复/跟随统一由 renderDetail 处理（基于重建前记录的 prevScroll）。
// 本函数只负责填充事件 HTML 与绑定工具项折叠交互。
function renderEvents(t) {
  const el = document.getElementById('events');
  if (!el) return;
  const events = t.events || [];
  // 旧任务无 events 但有 output：兜底显示
  if (events.length === 0 && t.output) {
    el.innerHTML = `<div class="ev ev-text"><div class="ev-bubble">${escapeHtml(t.output)}</div></div>`;
    return;
  }
  el.innerHTML = events.map(renderEvent).join('');
  // 工具/结果/思考：点击 head 切换 body 的 collapsed（默认折叠约 2 行，点击展开全部）
  el.querySelectorAll('.ev-tool-use .ev-head, .ev-tool-result .ev-head, .ev-thinking .ev-head').forEach(h => {
    h.addEventListener('click', () => {
      const body = h.parentElement.querySelector('.ev-body');
      const toggle = h.querySelector('.ev-toggle');
      if (body) {
        const collapsed = body.classList.toggle('collapsed');
        if (toggle) toggle.style.transform = collapsed ? 'rotate(-90deg)' : '';
      }
    });
  });
  // 「思考中...」占位徽章：提交后等待首次流式输出的过渡提示（呼吸灯 + 点渐变）
  if (showThinking(t)) {
    el.insertAdjacentHTML('beforeend', thinkingBadgeHTML());
  }
}

// thinkingBadgeHTML 「思考中...」占位徽章：呼吸灯 + 点渐变动效，缓解等待焦虑。
// 首次流式文本输出时由 applyTaskDelta 移除，无缝切换为流式文本。
function thinkingBadgeHTML() {
  return `<div class="ev ev-thinking-badge">
    <span class="tb-dot"></span>
    <span class="tb-text">思考中</span>
    <span class="tb-dots"><span></span><span></span><span></span></span>
  </div>`;
}

// showThinking 是否需要展示「思考中...」徽章：
// - 发送补充后置起 thinking 标记（首次流式文本清除）
// - 兜底：running/pending 且尚无任何事件/输出（如新建任务冷启动）
// - waiting_input（需决策）不展示：此时是请求用户决策而非思考，由决策横幅提示
function showThinking(t) {
  if (!t) return false;
  const active = t.status === 'running' || t.status === 'pending';
  if (!active) return false;
  if (state.thinking[t.id]) return true;
  return !(t.events || []).length && !t.output;
}

// removeThinkingBadge 移除详情区已渲染的「思考中...」徽章（首次流式文本到达时调用）。
function removeThinkingBadge() {
  const el = document.getElementById('events');
  const badge = el && el.querySelector('.ev-thinking-badge');
  if (badge) badge.remove();
}

function renderEvent(ev) {
  if (ev.type === 'thinking') {
    // 思考内容默认折叠（约 2 行），点击头部展开，与工具调用一致
    return `<div class="ev ev-thinking"><div class="ev-head"><span class="icon">💭</span>思考<span class="ev-toggle" style="transform:rotate(-90deg)">▾</span></div><div class="ev-body collapsed">${escapeHtml(ev.text || '')}</div></div>`;
  }
  // user 事件：用户提交的 prompt/续问，渲染为右对齐气泡
  if (ev.type === 'user') {
    return `<div class="ev ev-user"><div class="ev-bubble">${escapeHtml(ev.text || '')}</div></div>`;
  }
  if (ev.type === 'text') {
    const text = ev.text || '';
    // 续问标记 → 渲染为用户消息气泡（右对齐）
    // （兼容旧任务已持久化的「↻ 续问: 」前缀事件；新事件已改用 user 类型）
    if (text.startsWith('↻ 续问: ')) {
      return `<div class="ev ev-user"><div class="ev-bubble">${escapeHtml(text.slice('↻ 续问: '.length))}</div></div>`;
    }
    return `<div class="ev ev-text"><div class="ev-bubble">${escapeHtml(text)}</div></div>`;
  }
  if (ev.type === 'tool_use') {
    return `<div class="ev ev-tool-use">
      <div class="ev-head"><span class="icon">🔧</span>${escapeHtml(ev.tool_name || 'tool')}<span class="ev-toggle" style="transform:rotate(-90deg)">▾</span></div>
      <div class="ev-body collapsed">${renderInput(ev.input)}</div>
    </div>`;
  }
  if (ev.type === 'tool_result') {
    return `<div class="ev ev-tool-result ${ev.is_error ? 'error' : ''}">
      <div class="ev-head"><span class="icon">${ev.is_error ? '✗' : '↳'}</span>${escapeHtml(ev.tool_name || '结果')}${ev.is_error ? ' 失败' : ''}<span class="ev-toggle" style="transform:rotate(-90deg)">▾</span></div>
      <div class="ev-body collapsed">${escapeHtml(ev.result || '')}</div>
    </div>`;
  }
  return '';
}

// renderInput 健壮渲染 tool_use 的 input，避免 [object Object]。
// input 可能是: JSON 字符串、已解析对象、null。统一输出美化的 key: value 形式。
function renderInput(input) {
  if (!input) return '';
  let obj = input;
  if (typeof input === 'string') {
    try { obj = JSON.parse(input); } catch { return escapeHtml(input); }
  }
  if (typeof obj !== 'object' || obj === null) return escapeHtml(String(obj));
  // 平铺展示 key: value，每行一个，value 截断
  return Object.entries(obj).map(([k, v]) => {
    let val = typeof v === 'string' ? v : JSON.stringify(v);
    if (val.length > 200) val = val.slice(0, 200) + '...';
    return `<div class="input-row"><span class="input-key">${escapeHtml(k)}</span>: <span class="input-val">${escapeHtml(val)}</span></div>`;
  }).join('');
}

// --- 任务操作 ---
// opt 仅 choose action 用：用户点击的候选选项值（从按钮 data-option 取，不依赖 current_decision
// 避免点击时状态已变）。
async function onTaskAction(action, id, opt) {
  try {
    if (action === 'approve') {
      const t = state.tasks.find(x => x.id === id);
      await apiPost(`/tasks/${id}/intervene`, { kind: 'decision', decision_id: t?.current_decision?.id, choice: 'approve' });
    } else if (action === 'deny') {
      const t = state.tasks.find(x => x.id === id);
      await apiPost(`/tasks/${id}/intervene`, { kind: 'decision', decision_id: t?.current_decision?.id, choice: 'deny' });
    } else if (action === 'choose') {
      // 路径 B：选项值作为 append_prompt 发送 -> 后端 Resume 续跑
      if (!opt) return;
      await apiPost(`/tasks/${id}/intervene`, { kind: 'append_prompt', text: opt });
    } else if (action === 'cancel') {
      await apiPost(`/tasks/${id}/cancel`, {});
    }
  } catch (err) {
    alert(err.message);
  }
}

// --- 任务更多操作菜单（浮动 popover） ---
const taskMenu = () => document.getElementById('task-menu');

function openTaskMenu(anchorEl, taskId) {
  const m = taskMenu();
  if (!m) return;
  m.dataset.taskId = taskId;
  // 定位到按钮右下方
  const r = anchorEl.getBoundingClientRect();
  m.style.top = `${r.bottom + 4}px`;
  m.style.left = `${r.right}px`;
  // 防溢出：若右侧超出视窗，改为左对齐按钮左边
  const mw = 120; // 预估菜单宽
  if (r.right + mw > window.innerWidth - 8) {
    m.style.left = `${Math.max(8, r.left - mw)}px`;
  }
  m.classList.remove('hidden');
}

function closeTaskMenu() {
  const m = taskMenu();
  if (m) { m.classList.add('hidden'); m.dataset.taskId = ''; }
}

// 菜单项点击：删除
taskMenu()?.addEventListener('click', async (e) => {
  const btn = e.target.closest('[data-action]');
  if (!btn) return;
  const m = taskMenu();
  const id = m.dataset.taskId;
  closeTaskMenu();
  if (!id) return;
  if (btn.dataset.action === 'delete') {
    const t = state.tasks.find(x => x.id === id);
    const label = t?.prompt ? `"${t.prompt.slice(0, 30)}"` : '该任务';
    if (!confirm(`确认删除 ${label}？\n（运行中的任务会先取消，记录不可恢复）`)) return;
    try {
      await apiDelete(`/tasks/${id}`);
      // 后端会推 task_deleted 事件，WS handler 负责更新 state + render
    } catch (err) {
      alert(err.message);
    }
  }
});

// 点菜单外/ESC 关闭
document.addEventListener('click', (e) => {
  const m = taskMenu();
  if (!m || m.classList.contains('hidden')) return;
  if (!e.target.closest('#task-menu') && !e.target.closest('[data-action="more"]')) {
    closeTaskMenu();
  }
});
document.addEventListener('keydown', (e) => {
  if (e.key === 'Escape') {
    closeTaskMenu();
    closeSidebar(); // 移动端：ESC 也收起任务栏
  }
});

// upsertTask 将任务插入/更新到本地扁平列表（不存在则新增，存在则替换）。
function upsertTask(t) {
  const i = state.tasks.findIndex(x => x.id === t.id);
  if (i >= 0) state.tasks[i] = t;
  else state.tasks.push(t);
}

// applyTaskDelta 处理 task_delta 事件（M2 真流式）。
// 把增量文本累积进本地 task 状态（镜像后端 appendTextDelta：扩最后一个同类型 event 的 text，
// 类型不同则新建；非思考增量追加到 output），并对当前选中且正在查看的 task 直接增量追加 DOM，
// 不触发 render()/renderDetail() 全量重绘。找不到目标 DOM 时回退为仅更新本地状态
// （下次全量重绘时由持久化的累积文本正确还原）。
function applyTaskDelta(ev) {
  const t = state.tasks.find(x => x.id === ev.task_id);
  if (!t) return; // 未知任务（快照未到/已删除）：丢弃，等 task_updated 兜底
  const delta = ev.delta || {};
  const text = delta.text || '';
  const isThought = !!delta.is_thought;
  if (!text) return;

  // 1. 更新本地状态：与后端 appendTextDelta 逻辑一致
  const targetType = isThought ? 'thinking' : 'text';
  const events = t.events || (t.events = []);
  if (events.length > 0 && events[events.length - 1].type === targetType) {
    events[events.length - 1].text = (events[events.length - 1].text || '') + text;
  } else {
    events.push({ seq: events.length + 1, type: targetType, text });
  }
  if (!isThought) {
    t.output = (t.output || '') + text;
    // 首次流式文本输出：结束「思考中...」占位，无缝切换为流式文本
    if (state.thinking[ev.task_id]) {
      state.thinking[ev.task_id] = false;
      removeThinkingBadge();
    }
  }

  // 2. 仅对当前选中且正在查看的 task 做增量 DOM 追加；非选中任务只更新本地状态
  if (state.selectedId !== ev.task_id) return;
  appendDeltaDOM(text, isThought);
}

// appendDeltaDOM 把单个增量文本直接追加到详情区 DOM（M2 真流式，不全量重绘）。
// 目标：#events 最后一个子元素若为目标类型（ev-text/ev-thinking）则追加到其 bubble/body；
// 否则新建一个目标类型元素（保持跨类型切换后流式连续）。增量文本经 escapeHtml 后追加防 XSS。
// 用户原本在底部则追加后跟随滚动。找不到 #events 容器则静默（状态已更新，下次重绘正确）。
function appendDeltaDOM(text, isThought) {
  const eventsEl = document.getElementById('events');
  if (!eventsEl) return;
  const targetClass = isThought ? 'ev-thinking' : 'ev-text';
  const innerSelector = isThought ? '.ev-body' : '.ev-bubble';

  // 追加前判断用户是否在底部（决定追加后是否跟随滚动）
  const scroller = document.querySelector('.detail-content');
  const wasNearBottom = scroller ? (scroller.scrollHeight - scroller.scrollTop - scroller.clientHeight < 120) : false;

  let inner = null;
  const last = eventsEl.lastElementChild;
  if (last && last.classList.contains(targetClass)) {
    inner = last.querySelector(innerSelector);
  }
  if (!inner) {
    // 最后一个事件类型不同（或无事件）：新建目标类型元素，保证后续同类型增量继续流入
    const node = document.createElement('div');
    if (isThought) {
      // 思考块：流式期间 body 展开（无 collapsed）让用户看到逐字；head 保留折叠交互
      node.className = 'ev ev-thinking';
      node.innerHTML = `<div class="ev-head"><span class="icon">💭</span>思考<span class="ev-toggle">▾</span></div><div class="ev-body"></div>`;
    } else {
      node.className = 'ev ev-text';
      node.innerHTML = `<div class="ev-bubble"></div>`;
    }
    eventsEl.appendChild(node);
    inner = node.querySelector(innerSelector);
    // 思考 head 点击折叠交互（与 renderEvents 对齐）
    if (isThought) {
      const head = node.querySelector('.ev-head');
      head.addEventListener('click', () => {
        const body = node.querySelector('.ev-body');
        const toggle = head.querySelector('.ev-toggle');
        if (body) {
          const collapsed = body.classList.toggle('collapsed');
          if (toggle) toggle.style.transform = collapsed ? 'rotate(-90deg)' : '';
        }
      });
    }
  }
  if (!inner) return;
  // 增量文本 escapeHtml 后追加（防 XSS）
  inner.insertAdjacentHTML('beforeend', escapeHtml(text));

  // 跟随滚动：用户原本在底部时追加后滑到底
  if (wasNearBottom && scroller) {
    scroller.scrollTop = scroller.scrollHeight;
  }
}

// --- WebSocket ---
function connectWS() {
  const qs = token ? `?token=${encodeURIComponent(token)}` : '';
  const proto = location.protocol === 'https:' ? 'wss' : 'ws';
  state.ws = new WebSocket(`${proto}://${location.host}/api/ws${qs}`);
  state.ws.onmessage = (e) => {
    const ev = JSON.parse(e.data);
    // M2 真流式：内容增量逐字追加到本地状态 + DOM，不触发 render()/renderDetail() 全量重绘
    if (ev.type === 'task_delta') {
      applyTaskDelta(ev);
      return;
    }
    if (ev.type === 'snapshot') {
      state.tasks = ev.tasks || [];
      applyUrlSelection(); // 从 URL 恢复会话选中（刷新/深链接进入）
    } else if (ev.type === 'task_deleted') {
      state.tasks = state.tasks.filter(t => t.id !== ev.task_id);
      if (state.selectedId === ev.task_id) state.selectedId = null;
    } else if (ev.task) {
      upsertTask(ev.task);
      // 任务进入终态或需决策：清除「思考中...」标记（决策由横幅提示，不再展示思考态）
      if (['completed', 'failed', 'cancelled', 'waiting_input'].includes(ev.task.status)) delete state.thinking[ev.task.id];
    }
    render();
    // 选中任务有更新时，只重渲染详情（避免列表重绘打断）
    if (state.selectedId) renderDetail();
  };
  state.ws.onclose = () => setTimeout(connectWS, 1000);
}

// --- 新建任务 ---
// 项目列表：从已有任务派生（project_path 去重），按最近使用倒序。
// 所有项目一视同仁，无「已注册/历史」之分。
function recentProjects() {
  const m = new Map(); // groupKey -> {id, path, lastUsed}
  for (const t of state.tasks) {
    if (!t.project_path) continue;
    const key = groupKey(t.project_path);
    const e = m.get(key);
    const tsv = new Date(t.updated_at || t.created_at).getTime() || 0;
    if (!e) m.set(key, { id: t.project_id || '', path: t.project_path, lastUsed: tsv });
    else if (tsv > e.lastUsed) { e.lastUsed = tsv; e.path = t.project_path; e.id = t.project_id || e.id; }
  }
  return [...m.values()].sort((a, b) => b.lastUsed - a.lastUsed);
}

// 新建任务：进入空白详情页（清除选中）并聚焦输入框。项目选择在表单内必填，
// 创建成功后再切到新任务详情；补充会话（详情页底部输入框）不含项目选择。
function openNewTask() {
  // 从任务详情页进入新建态时，默认项目 = 当前详情任务的项目路径
  const cur = state.tasks.find(t => t.id === state.selectedId);
  state.newTaskDefaultPath = (cur && cur.project_path) || null;
  state.selectedId = null;
  render();
  renderDetail(); // 空白分支会 populate 项目下拉 + 显示表单
  syncUrl(); // 新建态 → URL 回到 base
  const input = document.getElementById('nt-prompt');
  if (input) input.focus();
  closeSidebar(); // 移动端：进入新建时收起任务栏
}

// 填充新建任务的「项目选择」下拉（从已有任务派生，按最近使用倒序）。
function populateProjectPicker() {
  const sel = document.getElementById('nt-project');
  if (!sel) return;
  const projects = recentProjects();
  sel.innerHTML = projects.map(h => {
    const label = h.id || h.path;
    return `<option value="path:${escapeHtml(h.path)}" data-path="${escapeHtml(h.path)}">${escapeHtml(label)}</option>`;
  }).join('');

  // 默认项目优先级：详情页当前任务的项目 > 鼠标悬停的项目组
  let defaultPath = state.newTaskDefaultPath || '';
  if (!defaultPath && state.hoverProjectPath) defaultPath = state.hoverProjectPath;
  if (defaultPath) {
    // 用归一化 key 匹配（groupKey 统一 / 与 \、去重复斜杠、忽略大小写），
    // 避免同项目正/反斜杠写法不同导致精确匹配失败、默认落到错误项目。
    const dk = groupKey(defaultPath);
    const hit = projects.find(h => groupKey(h.path) === dk);
    if (hit) sel.value = `path:${hit.path}`;
  }
  // 默认回到「选项目」模式
  setProjectMode('select');
}

// 项目选择模式切换：select 下拉 vs 自定义路径输入
function setProjectMode(mode) {
  const sel = document.getElementById('nt-project');
  const pathInput = document.getElementById('nt-project-path');
  const toggle = document.getElementById('nt-custom-toggle');
  if (mode === 'path') {
    sel.classList.add('hidden');
    pathInput.classList.remove('hidden');
    toggle.textContent = '选项目';
    toggle.dataset.mode = 'path';
    pathInput.focus();
  } else {
    pathInput.classList.add('hidden');
    pathInput.value = '';
    sel.classList.remove('hidden');
    toggle.textContent = '自定义路径';
    toggle.dataset.mode = 'select';
  }
}

document.getElementById('new-task-btn').addEventListener('click', openNewTask);

// 新建态 footer 交互：项目模式切换 + 提交 + Ctrl/Cmd+Enter 发送。
// 元素由 renderDetail 动态重建，故每次进入新建态渲染后重新绑定。
function bindNewTaskFooter() {
  const toggle = document.getElementById('nt-custom-toggle');
  if (toggle) toggle.addEventListener('click', () => {
    setProjectMode(toggle.dataset.mode === 'path' ? 'select' : 'path');
  });
  const send = document.getElementById('nt-send');
  const input = document.getElementById('nt-prompt');
  if (send && input) {
    send.addEventListener('click', submitNewTask);
    // 有内容才启用发送（icon 按钮），与追加输入框一致
    input.addEventListener('input', () => { send.disabled = !input.value.trim(); });
    input.addEventListener('keydown', (e) => {
      if ((e.ctrlKey || e.metaKey) && e.key === 'Enter') {
        e.preventDefault();
        send.click();
      }
    });
  }
  // 斜杠补全（commands + skills 分组），与追加输入框一致
  const ntAc = document.getElementById('nt-autocomplete');
  if (input && ntAc) attachAutocomplete(input, ntAc, acSources);
}

// 提交新建任务：项目必填（下拉选中或自定义路径），成功后切到新任务详情。
async function submitNewTask() {
  if (state.creatingTask) return; // 在途防重：请求未返回前忽略重复点击/回车
  const toggle = document.getElementById('nt-custom-toggle');
  const mode = toggle.dataset.mode || 'select';
  const body = { prompt: document.getElementById('nt-prompt').value };
  if (mode === 'path') {
    const p = document.getElementById('nt-project-path').value.trim();
    if (!p) { alert('请输入项目路径'); return; }
    body.project_path = p;
  } else {
    const sel = document.getElementById('nt-project');
    const val = sel.value;
    if (!val) { alert('请选择项目'); return; }
    // option value 形如 "path:<abs>"，取前缀后为绝对路径
    const prefix = 'path:';
    body.project_path = val.startsWith(prefix) ? val.slice(prefix.length) : val;
  }
  // 立即置在途标记 + 禁用按钮，覆盖整个 await 窗口（此前连点会重复建任务）
  const send = document.getElementById('nt-send');
  state.creatingTask = true;
  if (send) send.disabled = true;
  try {
    const task = await apiPost('/tasks', body);
    document.getElementById('nt-prompt').value = '';
    setProjectMode('select');
    // 创建成功：本地即刻加入列表并默认选中该任务详情（用 POST 返回值，不依赖 WS 推送，避免偶发不送达）
    upsertTask(task);
    state.thinking[task.id] = true; // 新建即进入「思考中...」占位态，首次流式文本后清除
    selectTask(task.id);
  } catch (err) {
    if (send) send.disabled = false; // 失败恢复可提交，允许重试
    alert(err.message);
  } finally {
    state.creatingTask = false;
  }
}

document.querySelectorAll('[data-close]').forEach(b => b.addEventListener('click', () => document.getElementById(b.dataset.close).classList.add('hidden')));

// --- 移动端侧栏抽屉 ---
// 移动端 (<=720px)：侧栏默认收起，只展示详情页；汉堡按钮弹出任务列表；
// 点击任务/点非任务栏区域/ESC → 自动收起。
const mqMobile = window.matchMedia('(max-width: 720px)');
function isMobile() { return mqMobile.matches; }

function setSidebar(open) {
  const sidebar = document.getElementById('sidebar');
  const backdrop = document.getElementById('sidebar-backdrop');
  const toggle = document.getElementById('sidebar-toggle');
  sidebar.classList.toggle('open', open);
  backdrop.classList.toggle('show', open);
  toggle.classList.toggle('active', open);
  toggle.setAttribute('aria-expanded', String(open));
}
function closeSidebar() {
  if (!isMobile()) return;
  setSidebar(false);
}
function openSidebar() {
  if (!isMobile()) return;
  setSidebar(true);
}
// 断点切换回桌面时强制复位（去掉 open 状态，避免残留 transform）
// 老 iOS Safari (≤12) 的 MediaQueryList 只有 addListener，这里兼容两者
if (typeof mqMobile.addEventListener === 'function') {
  mqMobile.addEventListener('change', onBreakpointChange);
} else if (typeof mqMobile.addListener === 'function') {
  mqMobile.addListener(onBreakpointChange);
}
function onBreakpointChange(e) {
  if (!e.matches) setSidebar(false);
}

document.getElementById('sidebar-toggle').addEventListener('click', () => {
  const sidebar = document.getElementById('sidebar');
  setSidebar(!sidebar.classList.contains('open'));
});
// 点击遮罩（非任务栏范围）→ 收起
document.getElementById('sidebar-backdrop').addEventListener('click', closeSidebar);
// 汉堡按钮是 topbar 内唯一的交互元素，点击不会被遮罩误吞（按钮在遮罩上方 z-index 层级之外）

// --- 启动 ---
async function init() {
  if ('serviceWorker' in navigator) navigator.serviceWorker.register('/sw.js').catch(() => {});
  // Auth status poll — drives debug banner + binding-required prompts.
  try {
    const st = await apiGet('/auth/status');
    const banner = document.getElementById('debug-banner');
    if (banner) {
      if (st.debug) {
        banner.classList.remove('hidden');
      } else if (!st.bound) {
        banner.textContent = '⚠ 系统尚未绑定飞书管理员账号 — 请在内网访问 /api/auth/bind 完成';
        banner.classList.remove('hidden');
      }
    }
  } catch {}
  // 拉取补全数据源（commands + skills）
  try {
    const [{ commands }, { skills }] = await Promise.all([apiGet('/commands'), apiGet('/skills')]);
    acSources.commands = commands || [];
    acSources.skills = skills || [];
  } catch {}
  try {
    const { projects } = await apiGet('/tasks');
    state.tasks = projects.flatMap(g => g.tasks);
  } catch {}
  // 从 URL 恢复选中的会话（深链接/刷新进入 /session/<id>）；无匹配则回落新建态
  applyUrlSelection();
  render();
  renderDetail();
  connectWS();
}
init();
// 浏览器前进/后退：按 URL 恢复对应会话，或回到新建态（popstate 后 URL 已由浏览器更新，故跳过 syncUrl）
window.addEventListener('popstate', () => {
  const sid = sessionIdFromUrl();
  if (sid && state.tasks.some(t => t.id === sid)) {
    selectTask(sid, { noUrl: true });
  } else if (!sid && state.selectedId) {
    state.selectedId = null;
    render();
    renderDetail();
  }
});
