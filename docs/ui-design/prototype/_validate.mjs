import fs from 'node:fs';

const src = fs.readFileSync(new URL('./index.html', import.meta.url), 'utf8');
const errs = [], warns = [];

/* 1. 残留占位符 */
src.split('\n').forEach((l, i) => {
  if (/TODO|FIXME|__PLACEHOLDER|undefined泛|NaN/.test(l)) errs.push(`L${i + 1} 占位符: ${l.trim().slice(0, 90)}`);
});

/* 2. 内联 JS 语法 */
const scripts = [...src.matchAll(/<script[^>]*>([\s\S]*?)<\/script>/g)].map(m => m[1]);
scripts.forEach((s, i) => {
  try { new Function(s); } catch (e) { errs.push(`script#${i} 语法错误: ${e.message}`); }
});

/* 3. 标签平衡 */
const VOID = new Set(['area', 'base', 'br', 'col', 'embed', 'hr', 'img', 'input', 'link',
  'meta', 'param', 'source', 'track', 'wbr', 'path', 'circle', 'rect', 'use', 'line',
  'polyline', 'polygon', 'ellipse', 'stop']);
const stack = [];
const tagRe = /<(\/?)([a-zA-Z][a-zA-Z0-9-]*)([^>]*?)(\/?)>/g;
let m;
while ((m = tagRe.exec(src))) {
  const [, close, name, attrs, selfClose] = m;
  const ln = src.slice(0, m.index).split('\n').length;
  const n = name.toLowerCase();
  if (VOID.has(n) || selfClose === '/' || attrs.endsWith('/')) continue;
  if (close) {
    const top = stack.pop();
    if (!top) errs.push(`L${ln} 多余闭合 </${n}>`);
    else if (top.n !== n) errs.push(`L${ln} 标签错配: <${top.n}>(L${top.ln}) 被 </${n}> 关闭`);
  } else stack.push({ n, ln });
}
stack.forEach(t => errs.push(`L${t.ln} <${t.n}> 未闭合`));

/* 4. 图标引用（跳过 JS 动态拼接的 href，如 href="#' + ic + '" ） */
const symbols = new Set([...src.matchAll(/<symbol id="([^"]+)"/g)].map(m => m[1]));
const dynIcon = [];
[...src.matchAll(/<use href="#([^"]*)"/g)].forEach(m => {
  if (m[1].includes("' +") || m[1].includes('" +')) { dynIcon.push(m[1]); return; }
  if (!symbols.has(m[1])) errs.push(`图标未定义: #${m[1]}`);
});
/* 动态引用的图标名必须来自文件里出现的字符串字面量 */
dynIcon.forEach(() => {
  const lits = new Set([...src.matchAll(/'(i-[a-z0-9-]+)'/g)].map(x => x[1]));
  lits.forEach(l => { if (!symbols.has(l)) errs.push(`动态图标未定义: #${l}`); });
});
/* 未被引用的 symbol（仅提示） */
const used = new Set([...src.matchAll(/<use href="#([a-z0-9-]+)"/g)].map(m => m[1]));
symbols.forEach(s => { if (!used.has(s)) warns.push(`symbol #${s} 未被引用`); });

/* 5. CSS 变量：使用处必须在某处有定义（含单行多变量声明） */
const defined = new Set([...src.matchAll(/(^|[;{\s])(--[a-z0-9-]+)\s*:/gim)].map(m => m[2]));
const usedVars = new Set([...src.matchAll(/var\((--[a-z0-9-]+)/g)].map(m => m[1]));
usedVars.forEach(v => { if (!defined.has(v)) errs.push(`CSS 变量未定义: ${v}`); });

/* 6. $('#id') 目标存在 */
const ids = new Set([...src.matchAll(/\sid="([^"]+)"/g)].map(m => m[1]));
[...src.matchAll(/\$\('#([A-Za-z0-9_-]+)'\)/g)].forEach(m => {
  if (!ids.has(m[1])) errs.push(`#${m[1]} 在 DOM 中不存在`);
});
[...src.matchAll(/\$\$?\('(#[A-Za-z][A-Za-z0-9_-]*)'\)/g)].forEach(m => {
  const t = m[1].slice(1);
  if (!ids.has(t)) warns.push(`选择器 ${m[1]} 未命中任何 id`);
});

/* 7. 按钮钩子审计：每个 button 必须带一个行为 */
const BTN_OK = /data-(goto|nav|soon|act|approval|pane|task-status|theme|sec|scope|page|switch|create-robot|net)/;
[...src.matchAll(/<button\b[^>]*>/g)].forEach(b => {
  const tag = b[0];
  if (!BTN_OK.test(tag) && !/tsk-group-head/.test(tag)
      && !/id="task(ToggleAll|Clear)"/.test(tag)
      && !/id="dlg(Cancel|Refresh|ScanOk|ToManual|BackScan|ManualSave)"/.test(tag)   /* 弹层控件：由 id 直接绑定 */
      && !/sb-(group-head|proj|task)/.test(tag)
      && !/\bdisabled\b/.test(tag)) {
    warns.push(`button 无行为钩子: ${tag.replace(/\s+/g, ' ').slice(0, 110)}`);
  }
});

/* 8. 任务列表专项断言 */
const asserts = [
  [/id="navTree"/, '侧栏任务树容器'],
  [/id="navTaskHead"/, '任务列表分组头'],
  [/class="sb-group is-open" id="navTaskGroup"/, '任务列表默认展开（下面直接看到所有项目）'],
  [/var openProjects = \{\};/, '项目默认收起'],
  [/class="sb-proj"[^+]/, '项目行'],
  [/\.sb-task\b/, '任务行'],
  [/id="taskGroups"/, '移动端任务浏览器容器'],
  [/tsk-group-body/, '移动端折叠容器'],
  [/aria-expanded/, 'aria-expanded'],
  [/grid-template-rows: 0fr/, '折叠动画'],
  [/#page-tasks \.dash-head/, '任务浏览器页按窄屏布局'],
];
asserts.forEach(([re, label]) => { if (!re.test(src)) errs.push(`缺失: ${label}`); });

/* 9. 仪表盘必须只剩「待审批 + 本周概览」两区块 */
[['需要注意', '「需要注意」焦点组'], ['id="filterRow"', '状态 chip 过滤条'],
 ['task-grid', '「运行中」任务网格'], ['id="liveText"', '实时活动模拟'],
 ['ACTIVITY', '实时活动数据'], ['id="focusApprovalCount"', '已删除的待审批计数'],
 ['chip-go', 'chip 跳转箭头']].forEach(([s, label]) => {
  if (src.includes(s)) errs.push(`仪表盘残留 ${label}（${s}）`);
});
[/id="approvalZone"/, /id="focusCount"/, /id="insightHint"/, /class="insight"/].forEach((re, i) => {
  if (!re.test(src)) errs.push(`仪表盘缺失区块: ${['待审批容器', '待审批计数', '本周概览提示', '本周概览'][i]}`);
});

/* 10. 已删除的旧控件不得回归 */
[['taskProject', '空间筛选下拉'], ['taskSort', '排序下拉'],
 ['ST_ORDER', '死代码 ST_ORDER'], ['fProject', '死引用 fProject'],
 ['fSort', '死引用 fSort'], ['tcard', '已删除的 tcard 组件'],
 ['class="chip"', '已删除的状态 chip']].forEach(([s, label]) => {
  if (src.includes(s)) errs.push(`残留 ${label}（${s}）`);
});

/* 11. 变更反馈：默认收起（桌面右栏）—— 若有人改回常驻展开，这里会拦住 */
[/id="fbRail"/, /id="fbCollapse"/, /id="fbPanel"/,
 /class="se-right is-collapsed"/, /session-grid\.is-fb-collapsed/,
 /var fbCollapsed = true;/].forEach((re, i) => {
  if (!re.test(src)) errs.push(`变更反馈缺失: ${['收起态入口 fbRail', '收起按钮', '面板容器',
    '右栏初始 is-collapsed（默认收起）', '收起态 grid 列', 'fbCollapsed 初始值'][i]}`);
});
[/\.viewport\.is-mobile \.fb-rail \{ display: none; \}/,
 /\.viewport\.is-mobile #fbCollapse \{ display: none; \}/].forEach((re, i) => {
  if (!re.test(src)) errs.push(`变更反馈缺失移动端规则: ${['隐藏贴边条', '隐藏收起按钮'][i]}`);
});
/* 收起态必须仍能「点开」—— fbRail 的展开行为不能丢 */
if (!/fbRail\.addEventListener\('click', function \(\) \{ fbCollapsed = false; applyFb\(\); \}\)/.test(src)) {
  errs.push('变更反馈：贴边条点击未绑定展开');
}
/* 「本轮变更」在收起态下必须是死链的反面：先展开再切 Tab */
if (!/fbCollapsed = false; applyFb\(\);\s*\n\s*if \(viewport\.classList\.contains\('is-mobile'\)\) \{ setMobilePane\('feedback'\); return; \}/.test(src)) {
  errs.push('变更反馈：data-goto-feedback 未先展开面板（收起态下会成为隐性死链）');
}

/* 12. 设置：结构、判据与"连接状态只能有一处" */
[/id="page-settings"/, /class="set-body"/,
 /<h3>审批与自动化<\/h3>/, /<h3>连接与账号<\/h3>/,
 /<h3>外观与偏好<\/h3>/, /<h3>数据与关于<\/h3>/].forEach((re, i) => {
  if (!re.test(src)) errs.push(`设置缺失: ${['设置页容器', '设置主体', '第一组「审批与自动化」',
    '「连接与账号」', '「外观与偏好」', '「数据与关于」'][i]}`);
});
/* 「通知与推送」已整组移除：事件×渠道的逐条配置对当前阶段是过度设计。
   与下面的 agent 断言一起守住，防止它被当成"漏掉的常规项"补回来。 */
if (/通知与推送/.test(src)) errs.push('设置：通知与推送组已移除，不得回归');
/* IM 绑定的单位是「机器人」，不是渠道 —— 渠道 chip 形态不得回归 */
[['chip-tg', '已移除的渠道 chip'], ['set-chans', '已移除的渠道容器'],
 ['data-chans', '已移除的渠道分组钩子']].forEach(([s, label]) => {
  if (src.includes(s)) errs.push(`设置残留 ${label}（${s}）`);
});
/* 管理员位是**同一张卡的两个状态**（data-state），不是两张卡。
   未配置 = 虚线框 + 空心点 + 「未配置」徽章；已配置 = 绿点 + 能力标记。
   已配置态**没有"管理员"徽章**：身份已由名字里的「管理员」承载，
   再挂一枚就是同一句话说两遍（信息不重复原则）。 */
[/class="agent-list"/, /id="agentAdmin" data-state="unset"/, /class="agent-add"/,
 /飞书 · 管理员/, /class="status-dot idle" data-only="unset"/,
 /badge neutral" data-only="unset">未配置</,
 /class="status-dot ok" data-only="ready"/, /class="cap-tag" data-only="ready"/,
 /data-only="unset" data-create-robot/, /data-only="ready" data-soon="机器人配置/].forEach((re, i) => {
  if (!re.test(src)) errs.push(`设置缺失: ${['机器人列表', '未配置的管理员位（data-state）', '继续创建入口',
    '管理员机器人', '未配置的空心状态点', '未配置徽章',
    '已配置的实心状态点', '已配置的能力标记',
    '未配置态的「一键创建」入口', '已配置态的「配置」入口'][i]}`);
});
/* 状态切换必须由 data-state 驱动，两种状态的互斥内容都标 data-only ——
   否则 JS 只能去改文案，文案一旦搬进 JS，两处就会各自漂移。 */
if (!/\.agent-item\[data-state="unset"\] \[data-only="ready"\]/.test(src)) {
  errs.push('设置：卡片状态未走 data-state / data-only（文案会被迫搬进 JS）');
}
/* 以下两条只扫 HTML 段：JS 里动态生成的行（appendRobot）天然是已配置态，
   拿它去套"静态标记"的规定是误判 —— 断言要看清自己扫的是哪一段。 */
const htmlSrc = src.slice(0, src.indexOf('<script>'));
/* 未配置时不存在"去配置"这一步 —— 「配置」只能出现在已配置态上 */
{
  const cfg = htmlSrc.match(/<button\b[^>]*data-soon="机器人配置[^>]*>/g) || [];
  if (!cfg.length) errs.push('设置：已配置态缺少「配置」入口');
  cfg.forEach(tag => {
    if (!/data-only="ready"/.test(tag)) {
      errs.push('设置：「配置」入口未标在已配置态上（还没配置的东西无从配置）');
    }
  });
}
/* 一键创建有两个落点（管理员位 + 继续创建），走同一个流程、同一个函数 —— 不多不少两处 */
{
  const n = (htmlSrc.match(/\sdata-create-robot(?=[\s>])/g) || []).length;
  if (n !== 2) errs.push(`设置：一键创建入口应为 2 处（管理员位 + 继续创建），实际 ${n}`);
  if (!/\[\s?data-create-robot\]'\)\.forEach/.test(src)) {
    errs.push('设置：两个创建入口没有共用同一套绑定（会退化成两条流程）');
  }
}
if (!/支持预设提示词/.test(src)) {
  errs.push('设置：一键创建未写明支持预设提示词');
}
/* 渠道是机器人的「属性」，不是「上级分类」：不得出现渠道分组头。
   一旦分组，就等于把刚删掉的"智能体"那层换个名字加回来。 */
if (/class="agent-group"/.test(src)) {
  errs.push('机器人列表出现了渠道分组头（渠道是属性，不是上级分类）');
}
/* 「机器人」是唯一层级：用户可见文案里不得再出现"智能体"这个别名。
   同一件事起两个名字，用户只会怀疑它们有区别 —— 别名一旦回流就等于又长出一层。
   注释不计：那里提它是为了说明"为什么不用它"。 */
{
  const visible = src.replace(/\/\*[\s\S]*?\*\//g, '').replace(/<!--[\s\S]*?-->/g, '');
  if (/智能体/.test(visible)) {
    errs.push('「智能体」别名回流：机器人是唯一层级，用户可见处不得两名并存');
  }
}
/* 管理员是"首个绑定"自动确立的角色，不是要用户先想清楚的配置项 —— 规则必须写明 */
if (!/首个绑定的自动成为管理员/.test(src)) {
  errs.push('设置：管理员机器人的产生规则未写明（首绑即管理员）');
}
/* 特权的归属：属于**绑定的账号**，不属于机器人（2026-09-11 定案）。
   写成"只有管理员机器人可发起"会把特权说成机器人的属性 —— 与实现（权限在人）冲突，
   会让用户对"谁能开隧道"形成错误预期，多机器人下直接变成误操作。 */
if (!/特权属于该机器人绑定的飞书账号/.test(src)) {
  errs.push('设置：未写明特权归属（特权属于机器人绑定的飞书账号，不属于机器人本身）');
}
if (!/承载管理员能力/.test(src)) {
  errs.push('设置：能力标记语义须为「承载管理员能力」（特权在账号，机器人只是承载者）');
}
/* 权限默认值写进入口本身：新建的不含隧道与 API 权限 */
if (!/仅管理员含隧道与 API 权限/.test(src)) {
  errs.push('设置：创建入口未写明权限默认值（仅管理员含隧道与 API 权限）');
}

/* 13. 一键创建弹层：原型里唯一的浮层交互。
   形态由**真实流程**决定（飞书 Device Flow，internal/larkreg）：
   POST /api/larkreg/start 拿授权链接 → 渲染成二维码 → 用户用飞书扫 → GET /api/larkreg/poll 轮询。
   所以弹层的主体是**二维码本身**，不是表单。 */
[/id="dlgCreate"/, /role="dialog"/, /aria-modal="true"/, /aria-labelledby="dlgCreateTitle"/,
 /aria-describedby="dlgCreateDesc"/, /id="dlgPrompt"/, /id="dlgCancel"/, /id="dlgRefresh"/,
 /id="dlgScanOk"/, /id="dlgSteps"/, /class="qr-box"/, /class="qr-img"/,
 /class="qr-veil qr-veil-ok"/, /id="dlgQrStatus"/, /id="dlgQrTtl"/, /data-stage="gen"/, /data-qr-ttl="/,
 /tabindex="-1"/].forEach((re, i) => {
  if (!re.test(src)) errs.push(`弹层缺失: ${['容器', 'dialog 语义', 'aria-modal', '标题关联',
    '描述关联', '预设提示词输入', '取消按钮', '重新生成二维码', '模拟扫码（原型演示入口）',
    '机器侧分步进度', '二维码容器', '二维码本体', '扫码区「已完成」遮罩', '状态行', '有效期倒计时',
    '状态开关 data-stage', '有效期秒数 data-qr-ttl', '容器可聚焦（读屏要念标题）'][i]}`);
});
/* 手动配置：扫码之外的兜底路径。它与扫码**共用同一个弹层**（同一份预设提示词、同一段落卡逻辑），
   只由 data-mode 切换 body 里的可见性 —— 开第二个弹层就会有两份落卡逻辑。 */
[/data-mode="scan"/, /class="dlg-manual"/, /id="dlgToManual"/, /id="dlgBackScan"/,
 /id="dlgManualSave"/, /id="dlgAppId"/, /id="dlgSecret"/, /id="dlgAuth"/,
 /data-when="webhook"/, /id="dlgManualErr"/].forEach((re, i) => {
  if (!re.test(src)) errs.push(`手动配置缺失: ${['模式开关 data-mode', '手动表单容器',
    '「手动配置」次要入口', '「返回扫码」', '提交按钮', 'App ID 输入', 'App Secret 输入',
    '接入方式选择', 'webhook 条件字段', '就地错误行'][i]}`);
});
/* 手动模式下扫码那套必须整体让位：码、倒计时、扫码三步、演示入口、分步进度都不该还挂着 */
['.qr-box', '.qr-meta', '.qr-how', '.qr-demo', '.dlg-steps'].forEach(sel => {
  if (!new RegExp(`\\[data-mode="manual"\\] ${sel.replace('.', '\\.')}\\b`).test(src)) {
    errs.push(`手动配置：切到手动后 ${sel} 没有让位（两套界面会叠在一起）`);
  }
});
/* 次要入口必须在页脚而不是扫码区里：**四态恒在** ——
   gen（码还没生成出来）与 dead（码已过期）时恰恰最需要它，放进扫码区就会跟着一起藏起来。 */
if (!/class="dlg-alt" id="dlgToManual"/.test(src)) {
  errs.push('手动配置：入口不是低强调的次要形态（它会跟主路径抢注意力）');
}
/* webhook 的两个字段是**条件存在**，不是灰着占位 —— 灰着的字段会让人怀疑自己是不是漏填了 */
if (!/\.dlg-manual \.dlg-field\[data-when="webhook"\] \{ display: none; \}/.test(src)) {
  errs.push('手动配置：webhook 字段不是条件隐藏（长连接下会灰着占位，像是在等用户填）');
}
/* 两条路必须共用落卡逻辑：手动保存要是自己写一套，两边的结果迟早不一样 */
if (!/function submitManual\(\)/.test(src) || !/function finishRobot\(msg\)/.test(src)) {
  errs.push('手动配置：没有与扫码共用同一段落卡逻辑（finishRobot）');
}
if (!/finishRobot\('已保存飞书凭据并应用配置'\)/.test(src)) {
  errs.push('手动配置：手动保存没有走 finishRobot（结果不会落到卡片上）');
}
/* 进行态不抽走扫码区：抽掉会让弹层高度从 ~430px 塌到 ~220px，视觉上猛跳一下。
   码确实没用了，所以换成「已完成」遮罩而不是整块消失。 */
if (/data-stage="busy"\] \.qr-box/.test(src)) {
  errs.push('弹层：进行态把扫码区整块抽走了（弹层高度会塌下去，视觉上跳一下）');
}
/* **没有"创建"提交按钮。** 这是本轮最容易被"顺手加回来"的东西，必须显式守住：
   创建发生在用户手机的飞书上，桌面上点按钮什么也不会发生。
   桌面端只做三件事：把码摆出来、说清现在在等谁、别浪费用户等的时间。 */
if (/id="dlgSubmit"|dlgSubmit/.test(src)) {
  errs.push('弹层：出现了"创建"提交按钮 —— 创建由手机扫码触发，桌面上点它没有任何作用');
}
/* 四个状态都要有归属：scan 是默认态，其余三个必须各有一条样式规则，
   否则那个状态会静默地"没有样式"（不报错，只是看起来不对） */
['gen', 'busy', 'dead'].forEach(st => {
  if (!new RegExp(`data-stage="${st}"`).test(src)) errs.push(`弹层：状态 ${st} 没有对应样式规则`);
});
/* 二维码必须深色模块 + 浅色底，且**不跟随主题** ——
   深色模式下一反转就扫不出来，所以两处颜色都写死（生产端返回的 PNG 同理）。 */
if (!/\.qr-img path \{ fill: #000; \}/.test(src)) {
  errs.push('弹层：二维码模块色跟随了主题（深色模式下反转会扫不出来）');
}
if (!/\.qr-box \{[^}]*background: #fff/.test(src)) {
  errs.push('弹层：二维码底色没固定成浅色（扫码器只认浅底深码）');
}
/* 有效期到点必须给得出重新生成，且**复用生成逻辑** —— 否则会退化成第二条码流程。
   码过期后不是"提示一下但仍能用"，是真的不能再扫了。 */
if (!/dlgRefresh\.addEventListener\('click', generateQr\);/.test(src)) {
  errs.push('弹层：失效后的「重新生成」没有复用 generateQr（会变成第二套二维码流程）');
}
/* 结论仍按"现在创建会得到什么"现算，不写死一句话：
   管理员还没确立时，两个入口进来得到的都是第一台。
   描述还要跟着**模式**走：措辞里的"扫码后"在手动模式下不成立 ——
   那段话是决策依据，留一句与实际不符的就是在骗人点头。 */
if (!/function outcomeHtml\(first, manual\)/.test(src)) {
  errs.push('弹层：结论没有按当前状态 + 当前模式现算（outcomeHtml）');
} else {
  if (!/飞书 · 管理员<\/b>/.test(src) || !/飞书 · 成员<\/b>/.test(src)) {
    errs.push('弹层：两种身份的结论文案不完整（首台 = 管理员，其后 = 成员）');
  }
  if (!/不含隧道与 API 权限/.test(src)) {
    errs.push('弹层：后续创建的权限默认值未写明（新建不含隧道与 API）');
  }
  if (!/manual \? '填入一个已有飞书自建应用的凭据，' : '扫码后/.test(src)) {
    errs.push('弹层：描述没跟着模式变（手动模式下仍写着"扫码后"）');
  }
}
/* 「提交按钮该不该有」这一条，答案由**动作发生在哪一端**决定 ——
   扫码时动作在手机上，桌面上点按钮没用，所以没有；手动时动作就在本页，所以必须有。
   同一个弹层给出相反答案，不改判据。 */
if (!/id="dlgManualSave"/.test(src)) {
  errs.push('手动配置：有表单却没有提交按钮（动作在本页，必须能提交）');
}
/* 机器侧三步必须对应后端真实动作，不能是想当然的"创建应用 / 建隧道" */
['确认飞书授权结果', '保存飞书凭据', '应用配置'].forEach((t, i) => {
  if (!src.includes(t)) errs.push(`弹层：机器侧第 ${i + 1} 步与后端动作对不上（缺「${t}」）`);
});
/* 模拟扫码入口是原型专用的假入口（真实由 poll 轮询感知），必须自带「原型演示」标签，
   否则会被读成设计的一部分。 */
if (!/id="dlgScanOk"[\s\S]{0,400}原型演示/.test(src)) {
  errs.push('弹层：模拟扫码入口没标明是原型演示（会被误读成设计元素）');
}
/* 机器侧动作进行中不可关闭：留下半成品比多等两秒贵得多 */
if (!/if \(dlgBusy\) return;\s*\/\* 机器侧动作进行中不许关/.test(src)) {
  errs.push('弹层：机器侧动作进行中可以被关闭（会留下半成品）');
}
/* 收起的弹层必须真的不可聚焦：visibility 参与过渡就是为了这个 ——
   只写 opacity 的话，Tab 会跑进一个看不见的弹层里。 */
if (!/\.dlg-scrim \{[^}]*visibility: hidden/.test(src)) {
  errs.push('弹层：收起态未用 visibility 隐藏，Tab 会跑进看不见的弹层');
}
/* 遮罩关闭用 mousedown：用 click 会在"框内拖选、松手落在遮罩上"时误关并丢掉输入 */
if (!/addEventListener\('mousedown', function \(e\) \{ if \(e\.target === dlg\)/.test(src)) {
  errs.push('弹层：遮罩关闭未用 mousedown（拖选文本松手会误关）');
}
/* 定位必须挂在 .viewport 上：fixed 会跳出 overflow:hidden 跑到浏览器窗口上 */
if (!/\.dlg-scrim \{[^}]*position: absolute; inset: 0/.test(src)) {
  errs.push('弹层：未用 absolute 定位在 .viewport 内（fixed 会跑到设备框外）');
}
if (!/\.viewport \{[^}]*position: relative/.test(src)) {
  errs.push('弹层：.viewport 不是定位上下文（弹层会脱离设备框）');
}
/* 成功必须是"卡片就地变化 + 提示"，不能只有提示 —— 提示会消失，卡片不会 */
if (!/adminCard\.dataset\.state = 'ready';/.test(src)) {
  errs.push('弹层：创建成功后管理员卡未切到已配置态（结果只留在一个会消失的提示里）');
}
/* 排序断言：最能减少负担的一组必须排在最前（不是惯例的"外观"） */
{
  const iApproval = src.indexOf('<h3>审批与自动化</h3>');
  const iAppear = src.indexOf('<h3>外观与偏好</h3>');
  if (!(iApproval > -1 && iAppear > -1 && iApproval < iAppear)) {
    errs.push('设置：审批与自动化必须排在「外观与偏好」之前（按减少负担排序）');
  }
}
/* 主题默认必须是「跟随系统」，且偏好与生效值分离 */
[/data-theme-pref="system" aria-pressed="true"/, /var themePref = 'system';/,
 /function resolvedTheme/, /setThemePref\('system'\);/].forEach((re, i) => {
  if (!re.test(src)) errs.push(`主题缺失: ${['三态按钮（跟随系统默认选中）', 'themePref 初始值',
    '偏好→生效值解析', '初始化调用'][i]}`);
});
if (/data-theme-pref="light" aria-pressed="true"/.test(src)) {
  errs.push('主题：默认不能选中「浅色」（浅色优先 ≠ 默认浅色）');
}
/* 连接状态只能有一处：侧栏底部那一项的行尾。仪表盘头部不得再有副本。 */
if (/badge run[^>]*><span class="status-dot ok"[^>]*><\/span>实时连接正常/.test(src)) {
  errs.push('连接状态重复：仪表盘头部仍有「实时连接正常」徽章');
}
/* 设置页不得再承诺"尚未实现"的连接入口 */
if (/data-soon="设置/.test(src)) errs.push('设置仍是 data-soon 死链');
/* 设置必须真的可达：侧栏与底栏都要有 data-goto="settings" */
{
  const n = (src.match(/data-nav="settings" data-goto="settings"/g) || []).length;
  if (n < 2) errs.push(`设置入口不足：侧栏与底栏都应有 data-goto="settings"（实际 ${n} 处）`);
}
/* 已删除的孤儿样式不得回归 */
[/^\.done-toggle/m, /^\.done-list/m, /^\.done-item/m, /^\.empty \{/m,
 /^\.set-chans/m, /^\.chip-tg/m].forEach((re, i) => {
  if (re.test(src)) errs.push(`孤儿 CSS 回归: ${['.done-toggle', '.done-list', '.done-item', '.empty', '.set-chans', '.chip-tg'][i]}`);
});
/* 移除渠道 chip 后，样式、DOM、JS 三者必须同时消失（只删其一就是死代码） */
if (/chip-tg/.test(src)) errs.push('渠道 chip 残留：样式 / DOM / JS 未同步清理');
/* 能力标记：原型当前是未配置态，允许零引用（规格见 UI-DESIGN-SPEC §5.4.1 的状态表）；
   但一旦被引用就必须恰好一处 —— 只有管理员持有「隧道 / API」，多于一处即边界失守。 */
{
  const n = (src.match(/class="cap-tag"/g) || []).length;
  if (n > 1) errs.push(`能力标记出现 ${n} 处：只有管理员持有「隧道 / API」，多于一处即边界失守`);
}

/* ---------- 14. 仅内网可达的操作：外网下就地禁用 ----------
   /api/larkreg 的 start / poll / status / config **五个端点全部**挂在
   BindOpGateMiddleware 后面，外网一律 403 —— 连手动配置那条路也是死的。
   这正是"手动配置不是外网兜底"的原因，也是这里必须禁用入口的原因。
   正解：把入口禁掉、原因写在旁边；而不是让人点开弹层、看着二维码转完才发现走不通。 */
if (!/data-net="intranet"/.test(src)) errs.push('网络态：默认不是内网（原型默认态应为可用）');
if (!/id="netSwitch"/.test(src)) errs.push('网络态：没有可切换的演示开关（禁用态无法被检视）');
/* 禁用只能打在"要调这些接口的那几处"，**不做全局横幅** ——
   工作区、审批、外观都不依赖这些接口，不该跟着一起哑 */
if (!/id="agentAdmin"[^>]*data-needs-intranet/.test(src)) {
  errs.push('网络态：管理员卡没标 data-needs-intranet（禁用态无处可挂）');
}
if (!/id="agentAdd"[^>]*data-needs-intranet/.test(src)) {
  errs.push('网络态：底部创建入口没标 data-needs-intranet（只禁一半 = 留下"点这里试试"的洞）');
}
if (!/\.agent-item\[data-needs-intranet\] \.ag-warn/.test(src)) {
  errs.push('网络态：原因没长在被禁用的卡片旁边（被禁用的控件没有点击能替自己解释）');
}
if (!/需在内网访问 · 飞书接入接口不对外网开放/.test(src)) errs.push('网络态：没写明禁用原因');
/* 状态点必须跟着**名字**走：外网下卡片多一行原因，居中的点会掉到行间看着像标错了对象 */
if (!/\.agent-item > \.status-dot \{ align-self: flex-start/.test(src)) {
  errs.push('网络态：卡片状态点仍垂直居中（多一行原因后会掉到行间）');
}
/* 必须真的设 disabled，而不是只做视觉灰化：灰化只是"看起来不能点"，
   读屏不会念、回车仍会触发 —— 说的是另一回事（与"状态以 aria 为唯一真相"同一原则） */
if (!/function applyNet\(\)/.test(src) || !/b\.disabled = !ok;/.test(src)) {
  errs.push('网络态：只做了视觉灰化，没有真的 disabled（读屏念不出来）');
}
/* 运行时追加的机器人卡也要跟上网络态，否则它就是个漏网入口 —— 正是本轮要消灭的东西 */
if (!/agentList\.insertBefore\(el, agentAddBtn\);\s*applyNet\(\);/s.test(src)) {
  errs.push('网络态：运行时新增的机器人卡没跟上网络态（漏网入口）');
}
/* 窄屏默认把底部入口的说明藏掉；但外网下那行字**就是原因本身**，必须放出来。
   放出来的只能是"原因"那一行 —— 规则没限定的话，会把无关的补充说明一起放出来（已踩过）。 */
if (!/\.viewport\.is-mobile \.agent-add\[data-needs-intranet\] \.hint\[data-net-only="blocked"\]/.test(src)) {
  errs.push('网络态：窄屏的外网说明没限定到"原因"那一行（会把无关的补充说明一起放出来）');
}
/* 原因必须是**唯一要让人看懂的东西**，不能被禁用态的视觉处理一起削弱 ——
   整块降透明度会把橙色一起淡到 2.4:1，等于把原因写没了。 */
if (/\.agent-add\[disabled\] \{[^}]*opacity/.test(src)) {
  errs.push('网络态：禁用态整块降透明度会把原因一起淡掉（读不清，而它正是唯一要让人看懂的东西）');
}

console.log('=== 校验结果 ===');
console.log(`错误 ${errs.length} 项 / 提示 ${warns.length} 项`);
if (errs.length) { errs.forEach(e => console.log('  ✗ ' + e)); }
if (warns.length) { warns.forEach(w => console.log('  ! ' + w)); }
if (!errs.length) console.log('  ✓ 全部通过');
process.exit(errs.length ? 1 : 0);
