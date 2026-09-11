/* ESM 不认 NODE_PATH，直接按绝对路径加载项目内已装的 playwright（CJS 包需取 default） */
const _pw = await import('file:///G:/my_workspace/go/pieqi/web/node_modules/playwright/index.js');
const { chromium } = _pw.chromium ? _pw : _pw.default;
import fs from 'node:fs';
import path from 'node:path';

const HTML = 'G:/my_workspace/go/pieqi/docs/ui-design/prototype/index.html';
const SHOTS = 'G:/my_workspace/go/pieqi/docs/ui-design/prototype/_shots';
fs.mkdirSync(SHOTS, { recursive: true });

const errs = [];
const ok = (cond, label, extra = '') => {
  console.log((cond ? '  ✓ ' : '  ✗ ') + label + (extra ? ' → ' + extra : ''));
  if (!cond) errs.push(label + (extra ? ' → ' + extra : ''));
};

const CHROME = 'C:/Program Files/Google/Chrome/Application/chrome.exe';
const browser = await chromium.launch({ executablePath: CHROME, headless: true });
const page = await browser.newPage({ viewport: { width: 1440, height: 900 } });
page.on('pageerror', e => errs.push('页面 JS 异常: ' + e.message));
page.on('console', m => { if (m.type() === 'error') errs.push('控制台错误: ' + m.text()); });

await page.goto('file:///' + HTML);
await page.waitForTimeout(400);
/* 冷启动原始态（项目全收起），这是用户打开原型看到的第一眼 */
await page.screenshot({ path: path.join(SHOTS, '0-initial.png') });

console.log('=== 0. 视图容器内不得有游离文本节点 ===');
/* 历史事故：早期编辑吃掉过 `</sect` 与 `</nav` 的标签头，留下 "ion>" 和 ">" 两个游离文本，
   浏览器会当成普通文字渲染成可见字符 —— 而标签配对检查查不出来（配对仍然平衡）。
   这里直接按 DOM 查：视口内除脚本/样式外，不应存在"有尺寸的非空文本节点"的直系子元素。 */
const stray = await page.evaluate(() => [...document.querySelector('#viewport').childNodes]
  .filter(n => n.nodeType === 3 && n.textContent.trim())
  .map(n => n.textContent.trim()));
ok(stray.length === 0, '视口内无游离文本（如 ">" / "ion>"）', stray.map(s => JSON.stringify(s)).join(' ') || '0 处');

console.log('=== 1. 侧栏任务树：任务列表下面直接展示所有项目 ===');
ok(await page.getAttribute('#navTaskHead', 'aria-expanded') === 'true', '「任务列表」默认展开（不是点击才出项目）');
ok(await page.locator('#navTaskGroup').evaluate(el => el.classList.contains('is-open')), '分组容器带 is-open');

const projRows = await page.locator('#navTree .sb-proj').count();
ok(projRows === 3, '树下直接列出全部 3 个项目', `实际 ${projRows}`);

const taskRows0 = await page.locator('#navTree .sb-task').count();
ok(taskRows0 === 0, '项目默认收起 → 一条任务都不出现', `实际 ${taskRows0}`);

const counts = (await page.locator('#navTree .sb-proj .cnt-num').allTextContents()).map(s => s.trim());
ok(JSON.stringify(counts) === JSON.stringify(['5', '4', '3']), '每个项目显示任务数', counts.join('/'));

ok((await page.textContent('#navTaskCount')).trim() === '12', '分组头显示任务总数 12');

const sbTaskOutside = await page.locator('.sb-task').count();
ok(sbTaskOutside === 0, '无「任务」导航页入口 —— 任务列表不是页面，是分组');
ok(!(await page.locator('#pageSwitch button[data-page="tasks"]').isVisible()),
   '原型工具条在桌面端不提供任务浏览器入口（该页只有移动端可达）');

console.log('=== 2. 点项目 → 展开该项目全部任务（按更新时间排序）===');
await page.click('#navTree .sb-proj[data-project="oms-service"]');
await page.waitForTimeout(300);
ok(await page.getAttribute('#navTree .sb-proj[data-project="oms-service"]', 'aria-expanded') === 'true', '项目 aria-expanded=true');
ok(await page.locator('#navTree .sb-tasks').count() === 1, '只有被点的项目展开');
const names = (await page.locator('#navTree .sb-task .nm').allTextContents()).map(s => s.trim());
const EXPECT = ['修复订单库存扣减不同步', '补齐支付回调单元测试', '排查支付回调重复入账', '重构用户权限中间件', '为订单列表补充分页与筛选'];
ok(JSON.stringify(names) === JSON.stringify(EXPECT), '项目内任务按最近更新升序', names.join(' | '));
/* 侧栏宽度有限，任务名是扫描目标 —— 不许再插列把它挤窄
   （插一个「相对时间」列会把 167px 压到 121px，实测 80% 任务名被截断） */
const minNameW = await page.evaluate(() => Math.min(
  ...[...document.querySelectorAll('#navTree .sb-task .nm')].map(n => n.clientWidth)));
ok(minNameW >= 155, '侧栏任务名可用宽度 ≥ 155px（守卫元数据列不得挤占主标签）', minNameW + 'px');
await page.screenshot({ path: path.join(SHOTS, '1-desktop-tree-open.png') });

console.log('=== 3. 点任务 → 主区展示该任务详情 ===');
await page.click('#navTree .sb-task[data-task="补齐支付回调单元测试"]');
await page.waitForTimeout(400);
ok(await page.isVisible('#page-session'), '主区切到会话（任务详情）页');
const sessName = (await page.textContent('#sessionTaskName')).trim();
ok(sessName === '补齐支付回调单元测试', '详情标题跟随所选任务', sessName);
const sessProj = (await page.textContent('#sessionProjectName')).trim();
ok(sessProj === 'oms-service', '详情里的项目名跟随所选任务', sessProj);
ok(await page.locator('#navTree .sb-task.is-active').count() === 1, '树中唯一高亮当前任务');
ok((await page.getAttribute('#navTree .sb-task.is-active', 'data-task')) === '补齐支付回调单元测试', '高亮的正是所点任务');

console.log('=== 3b. 变更反馈默认收起 ===');
ok(await page.locator('#pane-feedback').evaluate(el => el.classList.contains('is-collapsed')),
   '右侧「变更反馈」初始为收起态');
ok(!(await page.locator('#pane-feedback .fb-panel').isVisible()), '收起时面板内容不可见');
ok(await page.locator('#fbRail').isVisible(), '收起时露出竖向贴边条（展开入口）');
ok(await page.locator('#fbTabs').count() === 1, 'Tabs 仍在 DOM（只是被收起，不是删除）');
const fbW0 = await page.evaluate(() => document.querySelector('#pane-feedback').getBoundingClientRect().width);
ok(fbW0 <= 60, '收起态右栏宽度 ≤ 60px（宽度让给时间线）', fbW0.toFixed(0) + 'px');
const tlW0 = await page.evaluate(() => document.querySelector('#pane-timeline').getBoundingClientRect().width);
ok(tlW0 > 1000, '收起态时间线拿到几乎全部宽度', tlW0.toFixed(0) + 'px');
await page.screenshot({ path: path.join(SHOTS, '2-session-fb-collapsed.png') });

console.log('=== 3c. 点贴边条才展开 / 再点收起 ===');
await page.click('#fbRail');
await page.waitForTimeout(400);
ok(!(await page.locator('#pane-feedback').evaluate(el => el.classList.contains('is-collapsed'))), '点贴边条 → 展开');
ok(await page.locator('#pane-feedback .fb-panel').isVisible(), '展开后面板内容可见');
ok(!(await page.locator('#fbRail').isVisible()), '展开后贴边条隐藏（同一位置不叠两个控件）');
const fbW1 = await page.evaluate(() => document.querySelector('#pane-feedback').getBoundingClientRect().width);
ok(fbW1 > 380, '展开态右栏约 420px', fbW1.toFixed(0) + 'px');
ok(await page.getAttribute('#fbCollapse', 'aria-expanded') === 'true', '收起按钮 aria-expanded=true');
await page.screenshot({ path: path.join(SHOTS, '3-session-fb-expanded.png') });

await page.click('#fbCollapse');
await page.waitForTimeout(400);
ok(await page.locator('#pane-feedback').evaluate(el => el.classList.contains('is-collapsed')), '点收起按钮 → 回到收起态');
ok(await page.locator('#fbRail').isVisible(), '收起后贴边条重新出现');

console.log('=== 3d. 收起态不丢状态：切页再回来仍是收起 ===');
await page.click('#navTree .sb-task[data-task="补齐支付回调单元测试"]');
await page.waitForTimeout(350);
ok(await page.locator('#pane-feedback').evaluate(el => el.classList.contains('is-collapsed')),
   '再次进入详情仍是收起（不因切页被复位成展开）');

console.log('=== 3e. 「本轮变更」展开并直达变更 Tab（不是隐性死链）===');
await page.click('#page-session .turn-change[data-goto-feedback]');
await page.waitForTimeout(450);
ok(!(await page.locator('#pane-feedback').evaluate(el => el.classList.contains('is-collapsed'))),
   '收起态点「本轮变更」→ 面板自动展开');
ok((await page.getAttribute('#fbTabs button[data-tab="changes"]', 'aria-selected')) === 'true', '并切到「变更」Tab');
await page.click('#fbCollapse');
await page.waitForTimeout(350);

console.log('=== 4. 高亮是「位置」不是「历史」 ===');
await page.click('.sidebar .nav-item[data-nav="dashboard"]');
await page.waitForTimeout(300);
ok(await page.isVisible('#page-dashboard'), '回到仪表盘');
ok(await page.locator('#navTree .sb-task.is-active').count() === 0, '离开详情页后树中不再高亮');

console.log('=== 5. 仪表盘只保留「待审批」+「本周概览」 ===');
const body = await page.innerHTML('#page-dashboard');
[['需要注意', '「需要注意」焦点组'], ['运行中', '「运行中」区块'], ['chip-go', '状态 chip'],
 ['filterRow', '状态过滤条'], ['liveText', '实时活动']].forEach(([s, label]) => {
  ok(!body.includes(s), `仪表盘不含 ${label}`);
});
ok(await page.locator('#page-dashboard #approvalZone .approval-card').count() === 3, '待审批区有 3 张审批卡');
ok(await page.locator('#page-dashboard .insight .ins-cell').count() === 4, '本周概览有 4 格指标');
const secTitles = (await page.locator('#page-dashboard .sec-title').allTextContents()).map(s => s.trim());
ok(secTitles.length === 1 && secTitles[0].startsWith('本周概览'), '仪表盘只剩一个区块标题（本周概览）', secTitles.join(' | '));
await page.screenshot({ path: path.join(SHOTS, '2-dashboard.png') });

console.log('=== 6. 仪表盘 → 任务详情链路仍通 ===');
await page.click('#page-dashboard .ac-foot [data-goto="session"]');
await page.waitForTimeout(350);
ok(await page.isVisible('#page-session'), '「查看会话」进入详情');

console.log('=== 7. 「任务列表」分组本身可折叠 ===');
await page.click('.sidebar .nav-item[data-nav="dashboard"]');
await page.waitForTimeout(200);
await page.click('#navTaskHead');
await page.waitForTimeout(250);
ok(await page.getAttribute('#navTaskHead', 'aria-expanded') === 'false', '点分组头 → aria-expanded=false');
ok(!(await page.locator('#navTree').isVisible()), '折叠后项目树隐藏');
await page.click('#navTaskHead');
await page.waitForTimeout(250);
ok(await page.locator('#navTree .sb-proj').first().isVisible(), '再点恢复展开');

console.log('=== 8. 折叠状态跨容器共享（侧栏 ↔ 移动端任务浏览器）===');
/* 当前 oms-service 在侧栏是展开的（第 2 步点开过），切到移动端应保持 */
await page.click('#viewSwitch button[data-view="mobile"]');
await page.waitForTimeout(300);
ok(await page.locator('#pageSwitch button[data-page="tasks"]').isVisible(), '切到移动端后工具条出现任务浏览器入口');
await page.click('.tabbar button[data-goto="tasks"]');
await page.waitForTimeout(400);
ok(await page.isVisible('#page-tasks'), '底栏「任务」进入任务浏览器页');
ok(!(await page.locator('.sidebar').isVisible()), '移动端侧栏隐藏');
ok(await page.locator('#taskGroups .tsk-group.is-open').count() === 1, '侧栏展开的 oms-service 在移动端同样展开');
ok((await page.locator('#taskGroups .tsk-group.is-open .nm').textContent()).trim() === 'oms-service', '展开的正是 oms-service');
await page.click('#taskGroups .tsk-group:nth-child(2) .tsk-group-head');
await page.waitForTimeout(400);
ok(await page.locator('#taskGroups .tsk-group.is-open').count() === 2, '移动端可再展开一个项目');
await page.screenshot({ path: path.join(SHOTS, '3-mobile-tasks.png') });
await page.click('#pageSwitch button[data-page="dashboard"]');
await page.waitForTimeout(300);
await page.screenshot({ path: path.join(SHOTS, '4-mobile-dashboard.png') });

console.log('=== 9. 移动端点任务 → 详情 ===');
await page.click('.tabbar button[data-goto="tasks"]');
await page.waitForTimeout(350);
/* 前置：先确保第一个项目处于展开态（上一步的状态不假设，显式置位） */
if (!(await page.locator('#taskGroups .tsk-group').first().evaluate(el => el.classList.contains('is-open')))) {
  await page.click('#taskGroups .tsk-group:first-child .tsk-group-head');
  await page.waitForTimeout(350);
}
ok(await page.locator('#taskGroups .tsk-group:first-child .recent-item').first().isVisible(), '前置：第一个项目已展开');
const target = await page.locator('#taskGroups .tsk-group:first-child .recent-item').first().getAttribute('data-task');
await page.click('#taskGroups .tsk-group:first-child .recent-item:first-child');
await page.waitForTimeout(400);
ok(await page.isVisible('#page-session'), '移动端任务行进入详情页');
ok((await page.textContent('#sessionTaskName')).trim() === target, '移动端详情标题跟随所选任务', target);

console.log('=== 9b. 移动端「变更反馈」是整屏分段，不受桌面收起态影响 ===');
ok(!(await page.locator('#pane-feedback').evaluate(el => el.classList.contains('is-collapsed'))),
   '移动端不套用桌面收起态（桌面收起不应让手机端打不开反馈）');
await page.click('#mobSwitch button[data-pane="feedback"]');
await page.waitForTimeout(350);
ok(await page.locator('#pane-feedback .fb-body').isVisible(), '移动端切到「变更反馈」能看到面板内容');
ok(await page.locator('#fbTabs').isVisible(), '移动端 Tabs 可见');
ok(!(await page.locator('#fbRail').isVisible()), '移动端不显示竖向贴边条');
ok(!(await page.locator('#fbCollapse').isVisible()), '移动端隐藏「收起」按钮（无目标可收，留着就是死控件）');
ok(!(await page.locator('#pane-timeline').isVisible()), '移动端同一时刻只显示一个面');
await page.screenshot({ path: path.join(SHOTS, '5-mobile-feedback.png') });
await page.click('#mobSwitch button[data-pane="timeline"]');
await page.waitForTimeout(300);

console.log('=== 10. 设置：入口、结构、控件 ===');
await page.click('#viewSwitch button[data-view="desktop"]');
await page.waitForTimeout(300);
/* 10a. 连接状态从 3 处收敛为 1 处 */
ok(await page.locator('.sb-foot button').count() === 1, '侧栏底部只有一项（设置），不再有独立的连接状态行');
ok(await page.locator('#footConn').isVisible(), '连接状态作为行尾元数据留在设置项上');
ok(await page.locator('#page-dashboard .badge.run').count() === 0, '仪表盘头部不再有「实时连接正常」徽章');
/* 查渲染文本而不是源码 —— 源码里的注释不该被当成"重复文案"（注释不渲染） */
const connHits = (await page.evaluate(() => document.body.innerText)).match(/实时连接正常/g) || [];
ok(connHits.length === 0, '渲染后不再出现「实时连接正常」重复文案', `命中 ${connHits.length} 处`);

/* 10b. 点侧栏「设置」进设置页 */
await page.click('.sidebar .nav-item[data-nav="settings"]');
await page.waitForTimeout(350);
ok(await page.isVisible('#page-settings'), '侧栏「设置」进入设置页（不再是 Toast 死链）');
ok(await page.locator('.sidebar .nav-item[data-nav="settings"]').evaluate(el => el.classList.contains('is-active')),
   '侧栏设置项高亮（范围外的项永不高亮，这条已不适用）');
const groups = (await page.locator('#page-settings .set-group-head h3').allTextContents()).map(s => s.trim());
ok(JSON.stringify(groups) === JSON.stringify(['审批与自动化', '连接与账号', '外观与偏好', '数据与关于']),
   '四组齐全且顺序为「最能减少负担 → 最无害」', groups.join(' / '));
ok(!groups.includes('通知与推送'), '「通知与推送」已移除（事件 × 渠道的逐条配置属过度设计）');

/* 10c. 开关 */
const l0 = '#page-settings .switch[data-switch="l0"]';
ok(await page.getAttribute(l0, 'aria-checked') === 'true', 'L0 默认自动放行');
await page.click(l0);
await page.waitForTimeout(150);
ok(await page.getAttribute(l0, 'aria-checked') === 'false', '点开关 → aria-checked 翻转（状态以 aria 为唯一真相）');
await page.click(l0);
await page.waitForTimeout(150);
ok(await page.locator('#page-settings .switch[disabled]').count() === 2, 'L2 / L3 开关锁定不可配置');

/* 10c-bis. IM 机器人：绑定单位是机器人而非渠道；原型展示的是「未配置」态。
   「机器人」是唯一层级 —— 不再有"智能体"这个中间名，也不再有两套名字描述同一个东西。 */
ok(await page.locator('#page-settings .chip-tg').count() === 0, '渠道 chip 已彻底移除（样式 / DOM / JS 同步清理）');
ok(await page.locator('#page-settings .agent-item').count() === 1, '机器人列表只有管理员位');
ok((await page.textContent('#page-settings .ag-name')).trim() === '飞书 · 管理员', '管理员位是「飞书 · 管理员」');
/* 未配置态：虚线框 + 空心状态点 + 「未配置」徽章，动作是「一键创建」而不是「配置」。
   未配置 / 已配置是同一张卡的两个状态（data-state），差别说的是"要不要你动手"。 */
ok(await page.locator('#page-settings #agentAdmin[data-state="unset"]').count() === 1,
   '管理员位处于未配置态（虚线框）');
ok(await page.locator('#page-settings #agentAdmin .status-dot.idle').isVisible(),
   '未配置用空心状态点（不是绿点：这里还没有实体）');
ok(!(await page.locator('#page-settings #agentAdmin .status-dot.ok').isVisible()),
   '已配置的实心点此时不可见（两态内容互斥，不并排堆着）');
ok((await page.textContent('#page-settings #agentAdmin .badge')).trim() === '未配置', '徽章写明「未配置」');
{
  const btn = (await page.textContent('#page-settings #agentAdmin button[data-only="unset"]')).trim();
  ok(btn === '一键创建', '未配置态的动作是「一键创建」（还没配置，无从"去配置"）', btn);
}
/* 未配置就必须说清"绑定后会变成什么"，否则用户是盲点创建 */
ok(/自动成为管理员/.test(await page.textContent('#page-settings .agent-item .ag-desc')),
   '未配置态写明绑定后的结果（自动成为管理员）');
/* 「机器人」是唯一层级：用户可见处不得再出现"智能体"别名。
   查渲染文本（innerText）而不是 page.content() —— 源码里的注释会误判。 */
const setText = await page.innerText('#page-settings');
ok(!/智能体/.test(setText), '设置页文案里没有「智能体」这个多出来的名字');
ok(/首个绑定的自动成为管理员/.test(setText), '管理员由「首个绑定」自动确立，写明在组说明里');
/* 一键创建的第二个落点：继续创建 */
ok(await page.locator('#page-settings .agent-add').isVisible(), '「继续创建」入口在位');
{
  const addText = await page.textContent('#page-settings .agent-add');
  ok(/一键创建飞书机器人/.test(addText), '入口写明是「一键创建飞书机器人」');
  ok(/支持预设提示词/.test(addText), '入口写明创建时支持预设提示词');
  ok(/仅管理员含隧道与 API 权限/.test(addText), '权限默认值写明（只有管理员含隧道 / API）');
}
/* 10c-ter. 外网下就地禁用：/api/larkreg 的 start / poll / status / config 全挂在
   BindOpGateMiddleware 后面，外网一律 403 —— 连手动配置那条路也是死的
   （这正是它**不能**当"外网兜底"的原因）。所以正解是把入口禁掉 + 把原因写在旁边，
   而不是让人点开弹层、看着二维码转完，才发现根本走不通。 */
const adminBtn = '#page-settings #agentAdmin button[data-only="unset"]';
const addBtn = '#page-settings .agent-add';
ok(!(await page.locator(adminBtn).isDisabled()), '内网下创建入口可用（原型默认态）');
ok(!(await page.locator(addBtn).isDisabled()), '内网下底部创建入口可用');
await page.click('#netSwitch button[data-net="internet"]');
await page.waitForTimeout(200);
ok(await page.locator(adminBtn).isDisabled(), '外网下管理员卡的创建入口被禁用');
ok(await page.locator(addBtn).isDisabled(),
   '外网下底部创建入口一并禁用（只禁一半 = 留下"点这里试试"的洞）');
/* 原因必须**就地**长在被禁用的控件旁边：它连点击都没有，没有 hover / toast 能替它说话 */
ok(await page.locator('#page-settings #agentAdmin .ag-warn').isVisible(), '卡片上就地写明原因');
const warnText = (await page.textContent('#page-settings #agentAdmin .ag-warn')).trim();
ok(/需在内网访问/.test(warnText), '原因说清了"为什么现在不能点"', warnText);
ok(/不对外网开放/.test(warnText), '原因指向接口层面的硬限制（不是"暂时不可用"这种含糊话）');
/* 门锁上了，不等于要把这扇门是干什么的藏起来 */
ok(/自动成为管理员/.test(await page.textContent('#page-settings #agentAdmin .ag-desc')),
   '禁用时仍保留原描述（不拿原因顶掉"它会变成什么"）');
/* 不做全局横幅：不依赖飞书接口的项照常可用 */
ok(!(await page.locator('#page-settings .set-row .ctl button[data-soon="切换工作区目录"]').isDisabled()),
   '工作区等不依赖飞书接口的项不受影响（禁用只打在需要内网的那几处）');
await page.screenshot({ path: path.join(SHOTS, '19-net-blocked.png') });
await page.click('#netSwitch button[data-net="intranet"]');
await page.waitForTimeout(200);
ok(!(await page.locator(adminBtn).isDisabled()), '切回内网后入口恢复可用');

/* 一键创建弹层：两个入口共用同一个 —— 差别只在"是不是第一台"，不在流程。
   这里只开合、不往下走：走完流程会改变卡片状态，留给 10f 单独一节。 */
await page.click('#page-settings .agent-item button[data-only="unset"]');
await page.waitForTimeout(320);
ok(await page.locator('#dlgCreate.is-open').count() === 1, '「一键创建」打开弹层（不是一句 Toast）');
ok(await page.getAttribute('#dlgCreate .dlg', 'role') === 'dialog'
   && await page.getAttribute('#dlgCreate .dlg', 'aria-modal') === 'true',
   '弹层带 dialog 语义（role=dialog + aria-modal）');
/* 形态由真实流程决定：飞书 Device Flow 是"给一个授权链接让用户扫"，
   所以主体只能是二维码。 */
ok(await page.locator('#dlgCreate svg.qr-img').count() === 1, '弹层主体是二维码');
{
  const d = await page.getAttribute('#dlgCreate .qr-img path', 'd');
  ok(d && d.length > 2000, '二维码是真实编码结果（不是手画的占位图案）', (d || '').length + ' 字符 path');
}
/* 这条是本轮的要点：**桌面上没有"创建"按钮** —— 创建发生在手机上，
   点按钮什么也不会发生，留着它只会误导（它也是最容易被顺手加回来的东西）。 */
ok(await page.locator('#dlgCreate #dlgSubmit').count() === 0,
   '弹层里没有「创建」按钮（创建由手机扫码触发，桌面上点它没有作用）');
/* 四态：打开先「生成中」——真实 start 接口要等链接出现，直接显示码是假装不花时间 */
ok(await page.getAttribute('#dlgCreate', 'data-stage') === 'gen', '打开即进入「生成中」');
ok(/正在生成/.test(await page.textContent('#dlgCreate #dlgQrStatus')),
   '生成中状态行有说明（不是一张静止的空白码）');
ok(await page.locator('#dlgCreate .qr-veil-gen').isVisible(), '生成中遮罩可见（码还没拿到）');
/* 等状态而不是等固定毫秒：遮罩是淡出 + visibility 延迟，卡在过渡中间会误判 */
await page.waitForSelector('#dlgCreate[data-stage="scan"]', { timeout: 5000 });
await page.waitForTimeout(300);
ok(await page.getAttribute('#dlgCreate', 'data-stage') === 'scan', '链接就绪后转入待扫码态');
ok(await page.locator('#dlgCreate .qr-veil-gen').evaluate(el => getComputedStyle(el).visibility) === 'hidden',
   '待扫码时生成中遮罩已撤（不能盖着码）');
{
  const ttl = (await page.textContent('#dlgCreate #dlgQrTtl')).trim();
  ok(/^\d+:\d\d 后失效$/.test(ttl), '有效期倒计时按接口的 expire_in 显示', ttl);
}
ok(!(await page.locator('#dlgCreate #dlgRefresh').isVisible()),
   '待扫码态不显示「重新生成」（码还在，重生成是浪费）');
/* 扫码模式下"一键"才成立：要用户给的输入只有预设提示词一项。
   数的是**可见**的输入 —— 手动表单的字段在 DOM 里，但此刻不该占位。 */
ok(await page.locator('#dlgCreate .dlg-input:visible').count() === 1,
   '扫码模式下唯一要用户给的输入只有预设提示词（字段一多，"一键"就没了）');
ok(!(await page.locator('#dlgCreate .dlg-manual').isVisible()),
   '扫码模式下手动表单不显形（两套界面不叠着）');
ok(await page.locator('#dlgCreate #dlgToManual').isVisible(),
   '扫码区下方给出「手动配置」次要入口（码走不通时的旁门）');
ok(await page.getAttribute('#dlgCreate', 'data-mode') === 'scan', '默认进的是扫码模式');
ok(/管理员/.test(await page.textContent('#dlgCreate #dlgCreateDesc')),
   '首台创建写明得到的是管理员身份');
ok(/隧道/.test(await page.textContent('#dlgCreate #dlgCreateDesc')),
   '首台创建写明含隧道与 API 权限');
ok(/自建应用/.test(await page.textContent('#dlgCreate #dlgCreateDesc')),
   '写明扫码会在飞书组织内新建一个自建应用（这是要用户点头的事，不能瞒着）');
ok(await page.evaluate(() => !!document.querySelector('#dlgCreate').contains(document.activeElement)),
   '打开后焦点在弹层内（键盘用户不会还留在背后的页面上）');
/* 焦点停在容器上时 Shift+Tab 不能退到遮罩背后 —— 容器不在焦点陷阱的列表里，
   不显式拦就会漏出去（打开时我们正是把焦点放在容器上）。 */
await page.evaluate(() => document.querySelector('#dlgCreate .dlg').focus());
await page.keyboard.press('Shift+Tab');
ok(await page.evaluate(() => !!document.querySelector('#dlgCreate').contains(document.activeElement)),
   '焦点在容器上时 Shift+Tab 不会退到遮罩背后');
await page.keyboard.press('Escape');
await page.waitForTimeout(320);
ok(await page.locator('#dlgCreate.is-open').count() === 0, 'Esc 关闭弹层');
ok(await page.evaluate(() => !!(document.activeElement && document.activeElement.hasAttribute('data-create-robot'))),
   '关闭后焦点归还给触发它的按钮（不会掉回页面顶部）');
/* 第二个落点打开的是同一个弹层，且结论按"当前状态"算而不是按入口算 */
await page.click('#page-settings .agent-add');
await page.waitForTimeout(320);
ok(await page.locator('#dlgCreate.is-open').count() === 1, '「继续创建」打开的是同一个弹层（不是第二套流程）');
ok(/管理员/.test(await page.textContent('#dlgCreate #dlgCreateDesc')),
   '管理员还没确立时，两个入口进来都是第一台（结论按状态算，不按入口算）');
await page.keyboard.press('Escape');
await page.waitForTimeout(320);

/* 10d. 主题三态与两处视图的同源 */
ok(await page.getAttribute('#page-settings [data-theme-pref="system"]', 'aria-pressed') === 'true',
   '主题默认选中「跟随系统」');
ok(await page.evaluate(() => document.documentElement.dataset.themePref) === 'system', '偏好为 system');
ok(await page.getAttribute('#themeSwitch [data-theme-pref="system"]', 'aria-pressed') === 'true',
   '工具条与设置页同源（默认都是跟随系统）');
await page.click('#page-settings [data-theme-pref="dark"]');
await page.waitForTimeout(250);
ok(await page.evaluate(() => document.documentElement.dataset.theme) === 'dark', '设置页选深色 → data-theme=dark');
ok(await page.getAttribute('#themeSwitch [data-theme-pref="dark"]', 'aria-pressed') === 'true',
   '工具条同步高亮深色（同一份状态，不是第二份数据）');
ok(await page.getAttribute('#page-settings [data-theme-pref="system"]', 'aria-pressed') === 'false',
   '「跟随系统」让出选中态');
await page.click('#page-settings [data-theme-pref="light"]');
await page.waitForTimeout(250);
ok(await page.evaluate(() => document.documentElement.dataset.theme) === 'light', '三态里的「浅色」也能生效');
/* 截图前回到顶部：上面点过设置页里的按钮，浏览器会把焦点元素滚进视口 */
await page.evaluate(() => { document.querySelector('#page-settings .page-scroll').scrollTop = 0; });
await page.waitForTimeout(200);
await page.screenshot({ path: path.join(SHOTS, '8-settings-desktop.png') });

console.log('=== 10e. 移动端设置页 ===');
await page.click('#viewSwitch button[data-view="mobile"]');
await page.waitForTimeout(300);
await page.click('.tabbar button[data-goto="settings"]');
await page.waitForTimeout(400);
ok(await page.isVisible('#page-settings'), '底栏「设置」进入设置页');
ok(await page.locator('#page-settings .set-group').count() === 4, '移动端同样四组');
ok(await page.locator('#page-settings .agent-item').count() === 1, '移动端能看到管理员位');
ok(await page.locator('#page-settings .agent-item').first().isVisible(), '移动端机器人条目可见');
ok(await page.locator('#page-settings .agent-item .badge').first().isVisible(),
   '移动端「未配置」徽章没被换行挤掉（状态在窄屏依然看得出来）');
ok(!(await page.locator('#page-settings .agent-add .hint').first().isVisible()),
   '移动端隐藏「新建机器人」的补充说明（窄屏放不下，且非关键信息）');
/* 但**外网下那行字就是原因本身**，不能被这条隐藏规则一起吞掉 ——
   被禁用的控件没有点击，唯一能说话的只有旁边那行字，窄屏也不例外。 */
await page.click('#netSwitch button[data-net="internet"]');
await page.waitForTimeout(200);
ok(await page.locator('#page-settings .agent-add .hint[data-net-only="blocked"]').isVisible(),
   '移动端外网下底部入口的原因照样看得见（禁用态不能哑）');
ok(!(await page.locator('#page-settings .agent-add .hint[data-net-only="ok"]').isVisible()),
   '但只放"原因"那一行：无关的补充说明仍收着（规则没限定就会两条一起放出来）');
ok(await page.locator('#page-settings #agentAdmin .ag-warn').isVisible(),
   '移动端外网下卡片上的原因照样看得见');
{
  const sw = await page.evaluate(() => {
    const el = document.querySelector('#page-settings .set-body');
    return { sw: el.scrollWidth, cw: el.clientWidth };
  });
  ok(sw.sw <= sw.cw + 1, '外网下的原因文案没把窄屏撑出横向滚动', `${sw.sw} / ${sw.cw}`);
}
/* 截图前先把机器人那块滚进视野，否则拍到的是一屏跟本轮无关的内容 */
await page.evaluate(() => {
  const el = document.querySelector('#page-settings #agentAdmin');
  if (el) el.scrollIntoView({ block: 'center' });
});
await page.waitForTimeout(220);
await page.screenshot({ path: path.join(SHOTS, '20-net-blocked-mobile.png') });
await page.evaluate(() => { document.querySelector('#page-settings .page-scroll').scrollTop = 0; });
await page.waitForTimeout(160);
await page.click('#netSwitch button[data-net="intranet"]');
await page.waitForTimeout(200);
/* 移动端弹层降级为底部 Sheet（与 Session 的变更反馈同一套降级规则） */
await page.click('#page-settings .agent-item button[data-only="unset"]');
await page.waitForTimeout(320);
{
  const geo = await page.evaluate(() => {
    const vpEl = document.querySelector('#viewport');
    const vb = vpEl.getBoundingClientRect();
    const bw = parseFloat(getComputedStyle(vpEl).borderBottomWidth) || 0;
    const d = document.querySelector('#dlgCreate .dlg').getBoundingClientRect();
    /* inset:0 是相对 padding box 的，所以要减掉设备框的边框，否则会差 8px 而看起来像没贴底 */
    return { gap: Math.round(vb.bottom - bw - d.bottom),
             dw: Math.round(d.width), vw: Math.round(vpEl.clientWidth) };
  });
  ok(geo.gap <= 4, '移动端弹层贴底（底部 Sheet 形态）', geo.gap + 'px');
  ok(geo.dw >= geo.vw - 2, '移动端弹层横向撑满（不是被挤成居中的窄卡）', `${geo.dw} / ${geo.vw}`);
}
/* 二维码在窄屏不能被压扁或裁掉：它是这个弹层唯一必须真的能用的东西 */
{
  const q = await page.evaluate(() => {
    const b = document.querySelector('#dlgCreate .qr-box').getBoundingClientRect();
    return { w: Math.round(b.width), h: Math.round(b.height) };
  });
  ok(q.w === q.h && q.w >= 160, '移动端二维码保持正方形且不缩水（扫码器需要够大的码）', `${q.w}×${q.h}`);
}
await page.screenshot({ path: path.join(SHOTS, '15-create-mobile-sheet.png') });
await page.keyboard.press('Escape');
await page.waitForTimeout(320);
ok(await page.locator('#dlgCreate.is-open').count() === 0, '移动端 Esc 同样能关（键盘可达）');
/* 窄屏不得横向溢出：行内控件要换行到第二行，而不是把行撑宽 */
const setOverflow = await page.evaluate(() => {
  const el = document.querySelector('#page-settings .set-body');
  return { sw: el.scrollWidth, cw: el.clientWidth };
});
ok(setOverflow.sw <= setOverflow.cw + 1, '移动端设置页无横向溢出', `${setOverflow.sw} / ${setOverflow.cw}`);
const ctlWrapped = await page.evaluate(() => {
  const r = document.querySelector('#page-settings .set-row');
  const c = r.querySelector('.ctl');
  return c.getBoundingClientRect().width / r.getBoundingClientRect().width;
});
ok(ctlWrapped > 0.9, '移动端控件换行占满整行（拇指可点面积足够）', (ctlWrapped * 100).toFixed(0) + '%');
await page.evaluate(() => { document.querySelector('#page-settings .page-scroll').scrollTop = 0; });
await page.waitForTimeout(200);
await page.screenshot({ path: path.join(SHOTS, '9-settings-mobile.png') });

/* 10f. 一键创建：完整流程。会改变卡片状态，所以放在静态断言之后单独一节。
   这里要真走一遍四态：生成 → 待扫码 →（码过期）→ 重新生成 → 扫码确认 → 机器侧 → 落卡。 */
console.log('=== 10f. 一键创建（扫码流程四态）===');
await page.click('#viewSwitch button[data-view="desktop"]');
await page.waitForTimeout(320);
await page.click('.sidebar .nav-item[data-nav="settings"]');
await page.waitForTimeout(320);

/* 先把有效期调成 3 秒，好在一次运行里走到失效态 —— 真实 expire_in 是 600s，
   回归脚本不能真等十分钟。走的是同一个定时器、同一套过期处理，只是秒数小。 */
await page.evaluate(() => { document.querySelector('#dlgCreate').dataset.qrTtl = '3'; });
await page.click('#page-settings .agent-item button[data-only="unset"]');
await page.waitForTimeout(260);   /* 等遮罩淡入完，但还停在「生成中」（它持续 900ms） */
ok(await page.getAttribute('#dlgCreate', 'data-stage') === 'gen', '打开后先是「生成中」态');
await page.screenshot({ path: path.join(SHOTS, '11-create-gen.png') });
await page.waitForSelector('#dlgCreate[data-stage="scan"]', { timeout: 5000 });
await page.waitForTimeout(300);
await page.fill('#dlgCreate #dlgPrompt', '只在任务失败或需要我审批时找我。');
ok(await page.locator('#dlgCreate .qr-img').isVisible(), '待扫码态二维码真的显示出来了');
await page.screenshot({ path: path.join(SHOTS, '12-create-scan.png') });

/* 失效态：到期不是"提示一下但仍能用"，是真的不能再扫了 ——
   所以必须同时给出重新生成，否则用户会卡在一个没有出路的界面上。 */
await page.waitForSelector('#dlgCreate[data-stage="dead"]', { timeout: 6000 });
ok(/已失效/.test(await page.textContent('#dlgCreate #dlgQrStatus')), '状态行写明二维码已失效');
ok(await page.locator('#dlgCreate .qr-veil-dead').evaluate(el => getComputedStyle(el).visibility) === 'visible',
   '失效态用遮罩盖住码（摆着一张扫不出来的码，比收起来更糟）');
ok(await page.locator('#dlgCreate #dlgRefresh').isVisible(), '失效态给出「重新生成二维码」');
ok((await page.textContent('#dlgCreate #dlgQrTtl')).trim() === '', '失效后倒计时收起（不再是"还能用多久"）');
await page.screenshot({ path: path.join(SHOTS, '13-create-expired.png') });

/* 重新生成复用同一个 generateQr，回到正常的待扫码态 */
await page.evaluate(() => { document.querySelector('#dlgCreate').dataset.qrTtl = '600'; });
await page.click('#dlgCreate #dlgRefresh');
ok(await page.getAttribute('#dlgCreate', 'data-stage') === 'gen', '「重新生成」先回到生成中（不是瞬间换张码）');
await page.waitForSelector('#dlgCreate[data-stage="scan"]', { timeout: 5000 });
ok(!(await page.locator('#dlgCreate #dlgRefresh').isVisible()), '回到待扫码后「重新生成」收起');

/* 扫码确认之后的机器侧动作。真实环境这一步是 /api/larkreg/poll 轮询感知到的，
   原型没有服务端，所以这里点的是那个明标了「原型演示」的假入口。 */
await page.click('#dlgCreate #dlgScanOk');
await page.waitForTimeout(400);
ok(await page.getAttribute('#dlgCreate', 'data-stage') === 'busy', '扫码确认后进入机器侧进行态（不是瞬时关闭）');
ok(await page.locator('#dlgCreate .qr-box').isVisible(),
   '进行态保留扫码区（整块抽走会让弹层高度塌掉，视觉上跳一下）');
ok(await page.locator('#dlgCreate .qr-veil-ok').evaluate(el => getComputedStyle(el).visibility) === 'visible',
   '扫码区换成「已完成」遮罩（码不能再用了，摆着会让人以为还要再扫一次）');
ok(await page.locator('#dlgCreate .dlg-step').count() === 3, '机器侧三步：确认授权 → 保存凭据 → 应用配置');
ok(await page.locator('#dlgCreate .dlg-step.is-active').count() === 1, '机器侧分步推进');
await page.screenshot({ path: path.join(SHOTS, '14-create-busy.png') });
/* 进行中不可关闭：留下半成品比多等两秒贵得多 */
await page.keyboard.press('Escape');
await page.waitForTimeout(160);
ok(await page.locator('#dlgCreate.is-open').count() === 1, '机器侧动作进行中按 Esc 不关（不会留下半成品）');
await page.waitForTimeout(2400);   /* 起始 260ms + 三步 × 620ms */
ok(await page.locator('#dlgCreate.is-open').count() === 0, '创建完成后弹层自动关闭');
/* 提示是补充，不是结果本身：卡片就地变化 + 一句说明，两者都要有 */
{
  const t = (await page.locator('.toast').allTextContents()).join(' ').trim();
  ok(/已创建/.test(t) && /管理员/.test(t), '完成后提示写明创建了什么（不是一句"操作成功"）', t);
}
/* 结果必须落在卡片上，不能只留在一个会消失的提示里 */
ok(await page.locator('#page-settings #agentAdmin[data-state="ready"]').count() === 1,
   '管理员卡就地切成已配置态（实线框 + 绿点）');
ok(await page.locator('#page-settings #agentAdmin .status-dot.ok').isVisible(), '已配置态换成实心绿点');
ok(!(await page.locator('#page-settings #agentAdmin .badge').isVisible()), '已配置态不再显示「未配置」徽章');
ok(await page.locator('#page-settings #agentAdmin .cap-tag').isVisible(),
   '能力标记出现在管理员名旁（隧道 / API 现在只此一处）');
ok(/已设预设提示词/.test(await page.textContent('#page-settings #agentAdminDesc')),
   '填过的提示词留在卡片上（承诺的兑现要留在原地，不能只飘在提示里）');
ok(await page.locator('#page-settings .agent-item').count() === 1,
   '只创建了一台：管理员位是被"填上"的，不是又加了一行');
/* 第二轮：管理员已确立 → 这次得到的应该是普通成员。
   两个入口打开的是同一个弹层，结论由 outcomeHtml 按当前状态现算 —— 所以这里文案会变。 */
await page.click('#page-settings .agent-add');
await page.waitForSelector('#dlgCreate[data-stage="scan"]', { timeout: 5000 });
ok(/成员/.test(await page.textContent('#dlgCreate #dlgCreateDesc')),
   '管理员确立后创建得到的是成员（结论确实按当前状态算）');
ok(/不含隧道与 API/.test(await page.textContent('#dlgCreate #dlgCreateDesc')),
   '成员不含隧道与 API —— 写在下单前，而不是建完再告诉你');
await page.click('#dlgCreate #dlgScanOk');
await page.waitForTimeout(2600);
ok(await page.locator('#page-settings .agent-item').count() === 2, '第二次创建追加了一台机器人');
ok(await page.locator('#page-settings .cap-tag').count() === 1,
   '能力标记仍然只有一处 —— 成员没有隧道 / API，边界守住了');
ok(await page.locator('#page-settings .agent-item').nth(1).evaluate(el => !el.querySelector('.cap-tag')),
   '成员行不带任何"无权限"标记：差异靠缺席自解释，不必写出来');

/* 10f-bis. 手动配置：第二条接入路径。它要在**同一个弹层**里走完 ——
   开第二个弹层就会有两份落卡逻辑，两边迟早不一样。
   它兜的是"码走不通"（扫不了 / 已有现成应用 / 要 webhook），**不是外网**。 */
console.log('=== 10f-bis. 手动配置（兜底路径）===');
await page.click('#page-settings .agent-add');
await page.waitForSelector('#dlgCreate[data-stage="scan"]', { timeout: 5000 });
ok(await page.locator('#dlgCreate #dlgToManual').isVisible(), '扫码区下方给出「手动配置」次要入口');
await page.click('#dlgCreate #dlgToManual');
await page.waitForTimeout(300);
ok(await page.getAttribute('#dlgCreate', 'data-mode') === 'manual', '切到手动模式');
ok(!(await page.locator('#dlgCreate .qr-box').isVisible()), '手动模式下二维码让位（两套界面不叠着）');
ok(await page.locator('#dlgCreate .dlg-manual').isVisible(), '手动表单显形');
ok(/凭据/.test(await page.textContent('#dlgCreate #dlgCreateDesc')),
   '描述跟着模式变（不再写着"扫码后"——那段话是决策依据，不能与实际不符）');
ok(!(await page.locator('#dlgCreate #dlgRefresh').isVisible()),
   '手动模式下没有「重新生成二维码」（没有码可重生成）');
ok(await page.locator('#dlgCreate #dlgManualSave').isVisible(),
   '手动模式**有**提交按钮：动作就在本页（扫码模式没有，判据是动作发生在哪一端）');
ok(await page.locator('#dlgCreate #dlgPrompt').isVisible(),
   '手动模式下预设提示词仍在 —— 两条路产出的是同一件事，选项不该有差');
/* webhook 的两个字段是**条件存在**，不是灰着占位 —— 灰着的字段会让人怀疑自己漏填了 */
const whField = '#dlgCreate .dlg-manual .dlg-field[data-when="webhook"]';
ok(!(await page.locator(whField).first().isVisible()),
   '长连接模式下 Verify Token / Encrypt Key 不存在（不是灰着占位）');
await page.selectOption('#dlgCreate #dlgAuth', 'webhook');
await page.waitForTimeout(200);
ok(await page.locator(whField).first().isVisible(), '选 Webhook 后才出现 Verify Token / Encrypt Key');
await page.selectOption('#dlgCreate #dlgAuth', 'longconn');
await page.waitForTimeout(200);
ok(!(await page.locator(whField).first().isVisible()), '切回长连接后它们又消失（字段跟着模式走）');
await page.screenshot({ path: path.join(SHOTS, '18-create-manual.png') });
/* 缺字段不静默：就地报错 + 把焦点送过去。点一下什么都没发生是最差的一种"反馈" */
await page.click('#dlgCreate #dlgManualSave');
await page.waitForTimeout(250);
ok(await page.locator('#dlgCreate.is-open').count() === 1, '缺 App ID 时不静默提交（弹层不关）');
ok(await page.locator('#dlgCreate #dlgManualErr').isVisible(), '缺字段时就地报错（不是一句飘过的提示）');
ok(await page.evaluate(() => document.activeElement && document.activeElement.id === 'dlgAppId'),
   '报错同时把焦点送到那个字段');
await page.fill('#dlgCreate #dlgAppId', 'cli_a1b2c3d4e5f6');
await page.fill('#dlgCreate #dlgSecret', 'not-a-real-secret');
await page.click('#dlgCreate #dlgManualSave');
await page.waitForTimeout(250);
ok((await page.textContent('#dlgCreate #dlgManualSave')).trim() === '保存并应用…',
   '提交后按钮进入 loading（动作就在本页，按钮自身交代"在做什么"）');
await page.waitForTimeout(1500);
ok(await page.locator('#dlgCreate.is-open').count() === 0, '手动保存完成后弹层关闭');
ok(await page.locator('#page-settings .agent-item').count() === 3,
   '手动接入也落成了一张卡（与扫码共用同一段落卡逻辑）');
ok(await page.locator('#page-settings .cap-tag').count() === 1,
   '手动接入的仍是成员：能力标记还是只有管理员那一处');
/* 运行时追加的卡必须跟上网络态，否则它就是个漏网入口 —— 正是本轮要消灭的东西 */
await page.click('#netSwitch button[data-net="internet"]');
await page.waitForTimeout(200);
ok(await page.locator('#page-settings .agent-item').nth(2).locator('button').isDisabled(),
   '运行时新增的机器人卡也随网络态禁用（不是漏网入口）');
ok(await page.locator('#page-settings .agent-item').nth(2).locator('.ag-warn').isVisible(),
   '运行时新增的卡同样带上就地原因');
await page.click('#netSwitch button[data-net="intranet"]');
await page.waitForTimeout(200);

await page.waitForTimeout(600);
await page.screenshot({ path: path.join(SHOTS, '16-create-robot-done.png') });

console.log('=== 11. 深色主题 ===');
await page.click('#themeSwitch button[data-theme-pref="dark"]');
await page.waitForTimeout(250);
await page.click('#pageSwitch button[data-page="dashboard"]');
await page.waitForTimeout(250);
await page.screenshot({ path: path.join(SHOTS, '5-mobile-dark.png') });
await page.click('#viewSwitch button[data-view="desktop"]');
await page.waitForTimeout(300);
await page.screenshot({ path: path.join(SHOTS, '6-desktop-dark.png') });
/* 深色下也要确认收起态贴边条可读（深色 token 是独立的，不能靠浅色反推） */
await page.locator('#navTree .sb-task').first().click();
await page.waitForTimeout(400);
ok(await page.locator('#fbRail').isVisible(), '深色 + 桌面：贴边条仍可见');
const railColor = await page.evaluate(() => getComputedStyle(document.querySelector('#fbRail .lb')).color);
ok(railColor && railColor !== 'rgb(0, 0, 0)', '深色下贴边条标签有主题化颜色', railColor);
ok(await page.locator('#pane-feedback').evaluate(el => el.classList.contains('is-collapsed')), '深色下仍默认收起');
await page.click('.sidebar .nav-item[data-nav="settings"]');
await page.waitForTimeout(350);
await page.screenshot({ path: path.join(SHOTS, '10-settings-desktop-dark.png') });

/* 深色下二维码必须仍是"深码浅底"。反转的二维码扫不出来 ——
   这是全站唯一一处**故意不跟随主题**的配色，所以要有一条断言把它钉住，
   否则日后"统一深色 token"的改动会顺手把它一起反转掉。 */
await page.click('#page-settings .agent-add');
await page.waitForSelector('#dlgCreate[data-stage="scan"]', { timeout: 5000 });
ok(await page.locator('#dlgCreate .qr-img path').evaluate(el => getComputedStyle(el).fill) === 'rgb(0, 0, 0)',
   '深色主题下二维码模块仍是纯黑（不跟随主题）');
ok(await page.locator('#dlgCreate .qr-box').evaluate(el => getComputedStyle(el).backgroundColor) === 'rgb(255, 255, 255)',
   '深色主题下二维码底色仍是白（一反转就扫不出来了）');
await page.screenshot({ path: path.join(SHOTS, '17-create-scan-dark.png') });

await browser.close();
console.log('\n=== 结果 ===');
console.log(errs.length ? `失败 ${errs.length} 项:\n` + errs.map(e => '  - ' + e).join('\n') : '全部通过 ✓');
process.exit(errs.length ? 1 : 0);
