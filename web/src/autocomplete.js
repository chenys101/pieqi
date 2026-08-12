// autocomplete.js 共享斜杠补全：输入 / 后弹出分组菜单（Commands + Skills）。
//
// 用法：
//   import { attachAutocomplete } from './autocomplete.js';
//   attachAutocomplete(inputEl, acEl, { commands: [...], skills: [...] }, onPick);
// inputEl 失焦/输入时自动显隐；onPick(name) 在选择后调用（可选）。

let acIndex = -1;

// attachAutocomplete 给输入框绑定斜杠补全。
// sources: { commands: [{name,description}], skills: [{name,description}] }
function attachAutocomplete(input, ac, sources, onPick) {
  input.addEventListener('input', () => onInput(input, ac, sources, onPick));
  input.addEventListener('keydown', (e) => onKeydown(e, input, ac, onPick));
  input.addEventListener('blur', () => setTimeout(() => ac.classList.add('hidden'), 150));
}

function onInput(input, ac, sources, onPick) {
  const val = input.value;
  // 检测光标前最近的 /，且该 / 前是空格/行首
  const before = val.slice(0, input.selectionStart || val.length);
  const slash = before.lastIndexOf('/');
  if (slash < 0 || (slash > 0 && val[slash - 1] !== ' ' && val[slash - 1] !== '\n')) {
    ac.classList.add('hidden'); return;
  }
  const query = val.slice(slash + 1, before.length).toLowerCase();
  if (query.includes(' ')) { ac.classList.add('hidden'); return; }

  const cmdMatches = (sources.commands || []).filter(c => c.name.toLowerCase().includes(query)).slice(0, 8);
  const skillMatches = (sources.skills || []).filter(s => s.name.toLowerCase().includes(query)).slice(0, 8);
  if (cmdMatches.length === 0 && skillMatches.length === 0) {
    ac.classList.add('hidden'); return;
  }
  acIndex = -1;
  ac.innerHTML = renderGroups(cmdMatches, skillMatches);
  ac.classList.remove('hidden');
  ac.querySelectorAll('.autocomplete-item').forEach(item => {
    item.addEventListener('click', () => {
      insertItem(input, ac, item.dataset.name, onPick);
      if (onPick) onPick(item.dataset.name);
    });
  });
}

function renderGroups(cmds, skills) {
  let html = '';
  if (cmds.length) {
    html += `<div class="ac-group">命令</div>`;
    html += cmds.map(c =>
      `<div class="autocomplete-item" data-name="${esc(c.name)}"><div class="name">/${esc(c.name)}</div><div class="desc">${esc((c.description || '').slice(0, 60))}</div></div>`
    ).join('');
  }
  if (skills.length) {
    html += `<div class="ac-group">Skills</div>`;
    html += skills.map(s =>
      `<div class="autocomplete-item" data-name="${esc(s.name)}"><div class="name">/${esc(s.name)}</div><div class="desc">${esc((s.description || '').slice(0, 60))}</div></div>`
    ).join('');
  }
  return html;
}

function onKeydown(e, input, ac, onPick) {
  const items = ac.querySelectorAll('.autocomplete-item');
  if (ac.classList.contains('hidden') || items.length === 0) return;
  if (e.key === 'ArrowDown') {
    e.preventDefault();
    acIndex = (acIndex + 1) % items.length;
    highlight(ac, items);
  } else if (e.key === 'ArrowUp') {
    e.preventDefault();
    acIndex = (acIndex - 1 + items.length) % items.length;
    highlight(ac, items);
  } else if (e.key === 'Enter' && acIndex >= 0) {
    e.preventDefault();
    const name = items[acIndex].dataset.name;
    insertItem(input, ac, name, onPick);
    if (onPick) onPick(name);
  } else if (e.key === 'Escape') {
    ac.classList.add('hidden');
  }
}

function highlight(ac, items) {
  items.forEach((it, i) => it.classList.toggle('active', i === acIndex));
  // 滚动到可见
  if (acIndex >= 0 && items[acIndex]) {
    items[acIndex].scrollIntoView({ block: 'nearest' });
  }
}

// insertItem 把 /name 插入到光标前最近的 / 位置，光标定位到 name 后留空格让用户补参数。
function insertItem(input, ac, name, onPick) {
  const before = input.value.slice(0, input.selectionStart || input.value.length);
  const slash = before.lastIndexOf('/');
  const after = input.value.slice(input.selectionStart || input.value.length);
  input.value = input.value.slice(0, slash) + '/' + name + ' ' + after;
  ac.classList.add('hidden');
  input.focus();
  const pos = slash + name.length + 2; // /name + 空格
  input.setSelectionRange(pos, pos);
  // 触发 input 事件让外层更新发送按钮状态
  input.dispatchEvent(new Event('input'));
}

function esc(s) {
  return String(s).replace(/[&<>"']/g, c => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));
}

export { attachAutocomplete };
