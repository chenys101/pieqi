const _pw = await import('file:///G:/my_workspace/go/pieqi/web/node_modules/playwright/index.js');
const { chromium } = _pw.chromium ? _pw : _pw.default;

const browser = await chromium.launch({
  executablePath: 'C:/Program Files/Google/Chrome/Application/chrome.exe', headless: true
});
const page = await browser.newPage({ viewport: { width: 1440, height: 900 } });
await page.goto('file:///G:/my_workspace/go/pieqi/docs/ui-design/prototype/index.html');
await page.waitForTimeout(400);

/* 全部项目展开，逐个测量任务名是否被截断 */
await page.evaluate(() => {
  document.querySelectorAll('#navTree .sb-proj').forEach(b => {
    if (b.getAttribute('aria-expanded') === 'false') b.click();
  });
});
await page.waitForTimeout(400);

const report = await page.evaluate(() => {
  const rows = [...document.querySelectorAll('#navTree .sb-task')];
  return rows.map(r => {
    const nm = r.querySelector('.nm');
    const ago = r.querySelector('.ago');
    const cs = getComputedStyle(nm);
    return {
      name: nm.textContent,
      nameW: Math.round(nm.clientWidth),
      needW: Math.round(nm.scrollWidth),
      clipped: nm.scrollWidth > nm.clientWidth + 1,
      agoW: ago ? Math.round(ago.getBoundingClientRect().width) : 0,
      fs: cs.fontSize
    };
  });
});

const clipped = report.filter(r => r.clipped);
console.log(`任务行 ${report.length} 条，截断 ${clipped.length} 条（${Math.round(clipped.length / report.length * 100)}%）`);
console.log(`名称可用宽度 ≈ ${report[0].nameW}px，时间列 ≈ ${report[0].agoW}px`);
report.forEach(r => console.log(`  ${r.clipped ? '✂' : ' '} ${r.nameW}px/${r.needW}px  ${r.name}`));

/* 模拟去掉时间列后的可用宽度 */
const noAgo = await page.evaluate(() => {
  document.querySelectorAll('#navTree .sb-task .ago').forEach(e => e.style.display = 'none');
  return [...document.querySelectorAll('#navTree .sb-task')].map(r => {
    const nm = r.querySelector('.nm');
    return { name: nm.textContent, nameW: Math.round(nm.clientWidth), clipped: nm.scrollWidth > nm.clientWidth + 1 };
  });
});
const clipped2 = noAgo.filter(r => r.clipped);
console.log(`\n去掉时间列后：可用宽度 ≈ ${noAgo[0].nameW}px，截断 ${clipped2.length}/${noAgo.length} 条`);
noAgo.forEach(r => console.log(`  ${r.clipped ? '✂' : ' '} ${r.nameW}px  ${r.name}`));

await browser.close();
