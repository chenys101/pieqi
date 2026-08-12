// drawer.js 干预底部抽屉：Skill 胶囊 + / 自动补全 + approve/deny + 追加 prompt。

let skillsCache = [];
let currentTaskId = null;
let acIndex = -1;

export async function loadSkills(apiGet) {
  try {
    const { skills } = await apiGet('/skills');
    skillsCache = skills || [];
  } catch {
    skillsCache = [];
  }
}

export function openIntervene(task, apiPost) {
  currentTaskId = task.id;
  const sheet = document.getElementById('intervene-sheet');
  document.getElementById('iv-taskid').textContent = (task.id || '').slice(0, 8);

  // 决策区
  const decEl = document.getElementById('iv-decision');
  const approveBtn = document.getElementById('iv-approve');
  const denyBtn = document.getElementById('iv-deny');
  if (task.current_decision) {
    decEl.classList.remove('hidden');
    decEl.innerHTML = `⚠ <b>需决策</b>：${esc(task.current_decision.tool_name)} - ${esc(task.current_decision.summary)}`;
    approveBtn.classList.remove('hidden');
    denyBtn.classList.remove('hidden');
    approveBtn.onclick = () => doDecision(apiPost, task.current_decision.id, 'approve');
    denyBtn.onclick = () => doDecision(apiPost, task.current_decision.id, 'deny');
  } else {
    decEl.classList.add('hidden');
    approveBtn.classList.add('hidden');
    denyBtn.classList.add('hidden');
  }

  // Skill 胶囊
  const skillsEl = document.getElementById('iv-skills');
  skillsEl.innerHTML = skillsCache.slice(0, 20).map(s =>
    `<span class="capsule" data-skill="${esc(s.name)}"><span class="bolt">⚡</span> /${esc(s.name)}</span>`
  ).join('');
  skillsEl.querySelectorAll('.capsule').forEach(c => {
    c.addEventListener('click', () => {
      const input = document.getElementById('iv-text');
      input.value = `/${c.dataset.skill} ` + input.value;
      input.focus();
    });
  });

  // 输入 + 自动补全
  const input = document.getElementById('iv-text');
  input.value = '';
  const ac = document.getElementById('iv-autocomplete');
  input.oninput = () => onInput(input, ac);
  input.onkeydown = (e) => onKeydown(e, input, ac);

  // 发送追加
  document.getElementById('iv-send').onclick = async () => {
    const text = input.value.trim();
    if (!text) return;
    try {
      await apiPost(`/tasks/${currentTaskId}/intervene`, { kind: 'append_prompt', text });
      sheet.classList.add('hidden');
    } catch (err) { alert(err.message); }
  };

  sheet.classList.remove('hidden');
}

async function doDecision(apiPost, decisionId, choice) {
  try {
    await apiPost(`/tasks/${currentTaskId}/intervene`, { kind: 'decision', decision_id: decisionId, choice });
    document.getElementById('intervene-sheet').classList.add('hidden');
  } catch (err) { alert(err.message); }
}

function onInput(input, ac) {
  const val = input.value;
  // 检测 / 触发：光标前最近一个 / 后无空格
  const before = val.slice(0, input.selectionStart || val.length);
  const slash = before.lastIndexOf('/');
  if (slash < 0 || (slash > 0 && val[slash - 1] !== ' ' && val[slash - 1] !== '\n')) {
    ac.classList.add('hidden'); return;
  }
  const query = val.slice(slash + 1, before.length);
  if (query.includes(' ')) { ac.classList.add('hidden'); return; }
  const matches = skillsCache.filter(s => s.name.toLowerCase().includes(query.toLowerCase())).slice(0, 8);
  if (matches.length === 0) { ac.classList.add('hidden'); return; }
  acIndex = -1;
  ac.innerHTML = matches.map((s, i) =>
    `<div class="autocomplete-item" data-name="${esc(s.name)}" data-idx="${i}"><div class="name">/${esc(s.name)}</div><div class="desc">${esc((s.description || '').slice(0, 60))}</div></div>`
  ).join('');
  ac.classList.remove('hidden');
  ac.querySelectorAll('.autocomplete-item').forEach(item => {
    item.addEventListener('click', () => insertSkill(input, ac, item.dataset.name, slash));
  });
}

function onKeydown(e, input, ac) {
  const items = ac.querySelectorAll('.autocomplete-item');
  if (ac.classList.contains('hidden') || items.length === 0) return;
  if (e.key === 'ArrowDown') { e.preventDefault(); acIndex = (acIndex + 1) % items.length; highlight(ac, items); }
  else if (e.key === 'ArrowUp') { e.preventDefault(); acIndex = (acIndex - 1 + items.length) % items.length; highlight(ac, items); }
  else if (e.key === 'Enter' && acIndex >= 0) { e.preventDefault(); insertSkill(input, ac, items[acIndex].dataset.name, input.value.lastIndexOf('/')); }
  else if (e.key === 'Escape') { ac.classList.add('hidden'); }
}

function highlight(ac, items) {
  items.forEach((it, i) => it.classList.toggle('active', i === acIndex));
}

function insertSkill(input, ac, name, slashPos) {
  const after = input.value.slice(input.selectionStart || input.value.length);
  input.value = input.value.slice(0, slashPos) + '/' + name + ' ' + after;
  ac.classList.add('hidden');
  input.focus();
  const pos = slashPos + name.length + 2;
  input.setSelectionRange(pos, pos);
}

function esc(s) {
  return String(s).replace(/[&<>"']/g, c => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));
}
