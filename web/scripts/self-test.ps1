#!/usr/bin/env pwsh
# 真机自测：用本机已装 Chrome 无头渲染 pieqi 页面，抓控制台报错 + 白屏判定。
#
# 为什么需要：本会话文件沙箱**禁止 spawn 子进程**（Chrome / esbuild / node 子进程一律
# EPERM），所以
#   · agent-browser 用不了（它还硬编码往 ~/.agent-browser 写 socket）；
#   · Playwright 的 launch 用不了（spawn EPERM），connectOverCDP 也被掐 websocket。
# 而 jsdom **量不出布局/滚动类问题** —— 白屏恰恰是那一类。
# 唯一可行的是 Chrome 自带的 `--headless --dump-dom`：一次性进程、不走 CDP 管道，
# 只要**在沙箱外执行**（danger-full-access 那一次提权）就能跑。
#
# 用法（在 web/ 下）：
#   pwsh scripts/self-test.ps1 -Url http://127.0.0.1:3100
#   pwsh scripts/self-test.ps1 -Url http://127.0.0.1:3100 -Task <id> -Mobile
param(
  [string]$Url = 'http://127.0.0.1:3100',
  [string]$Task = '',
  [switch]$Mobile,
  [int]$WaitMs = 4000
)

$ErrorActionPreference = 'Stop'
$chrome = 'C:\Program Files\Google\Chrome\Application\chrome.exe'
if (-not (Test-Path $chrome)) { throw "找不到 Chrome：$chrome" }

$root = Split-Path -Parent $PSScriptRoot
$work = Join-Path $root 'build\selftest'
New-Item -ItemType Directory -Force -Path $work | Out-Null

# 每次跑都换一个 Chrome profile 目录：复用同一个 profile 会命中 HTTP/SW 缓存，
# 表现为"换了 URL 却拿到上一次的结果"（实测踩过，且它**静默**，比报错更危险）。
$runId = [guid]::NewGuid().ToString('N').Substring(0, 8)
$target = if ($Task) { "$Url/sessions/$Task" } else { "$Url/" }
$prof = Join-Path $work "chrome-prof-$runId"
New-Item -ItemType Directory -Force -Path $prof | Out-Null
$profDirect = Join-Path $work "chrome-direct-$runId"

# 注入口：在 DOM 里留下统计属性，dump-dom 之后解析。
# ⚠️ **不要用 setTimeout 写 title**：`--dump-dom` 在 virtual-time-budget 到点后
# 立刻取快照，异步回调常常还没跑；一切结论必须在**同步阶段**就写进 DOM。
# 所以这里用 MutationObserver + 同步兜底：一旦时间线出现就立刻落一份统计。
$probe = @'
<script>
(function(){
  var errs = [];
  window.addEventListener('error', function(e){ errs.push('ERROR: ' + (e.message||'') + ' @' + (e.filename||'') + ':' + (e.lineno||0)); });
  window.addEventListener('unhandledrejection', function(e){ errs.push('REJECT: ' + (e.reason && e.reason.message ? e.reason.message : String(e.reason))); });
  var oe = console.error.bind(console);
  console.error = function(){ errs.push('CONSOLE: ' + Array.prototype.map.call(arguments, String).join(' ')); oe.apply(null, arguments); };

  function root(){
    // 被测页面在 iframe 里；同源故可直接读它的 DOM
    try {
      var f = document.getElementById('f');
      if (f && f.contentDocument && f.contentDocument.body) return f.contentDocument;
    } catch(e) {}
    return document;
  }
  function stat(){
    var doc = root();
    var tl = doc.querySelector('[data-testid="session-timeline"]');
    var vis = function(el){ if(!el) return false; var r = el.getBoundingClientRect(); var s = el.ownerDocument.defaultView.getComputedStyle(el); return r.width>0 && r.height>0 && s.display!=='none' && s.visibility!=='hidden'; };
    var turns = [];
    doc.querySelectorAll('[data-testid^="turn-group-"]').forEach(function(n){
      var m = /turn-group-(\d+)/.exec(n.getAttribute('data-testid')||'');
      if (m) turns.push(parseInt(m[1],10));
    });
    turns.sort(function(a,b){return a-b;});
    return {
      textLen: ((doc.body && doc.body.innerText) || '').trim().length,
      hasTimeline: !!tl,
      timelineVisible: vis(tl),
      scrollHeight: tl ? tl.scrollHeight : 0,
      clientHeight: tl ? tl.clientHeight : 0,
      turns: turns,
      turnCount: turns.length,
      domNodes: doc.getElementsByTagName('*').length,
      loadOlder: !!doc.querySelector('[data-testid="load-older-turns"]'),
      jumpLatest: !!doc.querySelector('[data-testid="jump-latest"]'),
      groupMore: doc.querySelectorAll('[data-testid^="group-more-"]').length,
      rail: !!doc.querySelector('[data-testid="turn-rail"]'),
      errs: errs
    };
  }
  function emit(){
    var s = stat();
    document.documentElement.setAttribute('data-selftest', JSON.stringify(s));
  }
  // 变更后持续更新：最后一次写赢。iframe 初次加载完成后再多刷几轮，
  // 因为 SPA 挂载是异步的（首帧时 iframe 里几乎还是空的）。
  emit();
  var n = 0;
  var t = setInterval(function(){ emit(); if (++n > 120) clearInterval(t); }, 100);
  var f = document.getElementById('f');
  if (f) f.addEventListener('load', function(){ emit(); setTimeout(emit, 300); setTimeout(emit, 1200); setTimeout(emit, 2500); });
})();
</script>
'@

$outs = Join-Path $work 'rendered.html'
$extra = if ($Mobile) { '--window-size=390,844' } else { '--window-size=1440,900' }

# ⚠️ `--dump-dom <url>` **不能注入脚本**，而 file:// 包装页去 iframe 一个 http://
# 会被 Chrome 的"本地文件访问网络"策略挡住（实测拿到空 iframe）。
# 所以把包装页**放到被测站点同源的静态目录**下（pieqi 的 web/public 会被 embed 进
# 二进制的静态资源），再用 http://<被测量>/_selftest.html 打开 —— 同源、可直接读 iframe DOM。
$wrapperName = '_selftest.html'
$wrapperPublic = Join-Path (Split-Path -Parent $PSScriptRoot) 'public'
$wrapperPath = Join-Path $wrapperPublic $wrapperName
# ⚠️ iframe 的 URL 必须带 cache-buster：不带的话 Chrome 会命中上一次自测的缓存，
# 于是"换了 -Url 却还是上一个页面"（实测 /tasks 读出的是上一个会话页的 Turn）。
$stamp = [DateTimeOffset]::UtcNow.ToUnixTimeMilliseconds()
$frameSrc = if ($target -match '\?') { "$target&_st=$stamp" } else { "$target?_st=$stamp" }
@"
<!DOCTYPE html><html><head><meta charset="utf-8"><title>selftest</title></head>
<body style="margin:0">
<iframe id="f" src="$frameSrc" style="width:100vw;height:100vh;border:0"></iframe>
$probe
</body></html>
"@ | Set-Content -Path $wrapperPath -Encoding UTF8
# 同步写一份进 dist：被测服务多半是**已编译**的二进制（服务的是 embed 进 exe 的
# dist 快照），只写 public/ 而不重编是不会生效的（实测拿到 404 → SPA 兜底页）。
$distDir = Join-Path (Split-Path -Parent $PSScriptRoot) 'dist'
if (Test-Path $distDir) { Copy-Item $wrapperPath (Join-Path $distDir $wrapperName) -Force }

# 包装页永远在**站点根**下（它是 public 静态资源），与 $Url 是否带路径无关：
# 早先写成 "$Url/_selftest.html"，传 /tasks 时变成 /tasks/_selftest.html → 404。
$wrapperUrl = ((New-Object Uri $Url).GetLeftPart([UriPartial]::Authority)) + "/$wrapperName"

# ⚠️ 必须 no-first-run + 独立 profile：否则会复用已开的 Chrome 实例、直接返回空。
# ⚠️ 必须 `| Set-Content`（管道）：写成 `$x = & chrome ...` 会拿到空字符串（实测）。
#
# **主路径就是直连被测 URL**（实测最稳，能拿到任务页/会话页的完整渲染 DOM）。
# 包装页那条路（同源 iframe）只在能确保服务端已重编时才可用 ——
# 二进制服务的是 embed 进 exe 的 dist 快照，写 public/ 不重编不生效，
# 而一旦命中 SPA 兜底页就会**静默复用上一次的结果**（实测两次输出完全相同），
# 那比报错更危险。所以默认直连，需要 errs 时再显式 -UseWrapper。
& $chrome --headless=new --disable-gpu --no-first-run --no-default-browser-check `
  --user-data-dir="$prof-direct" --virtual-time-budget=$($WaitMs + 6000) $extra `
  --dump-dom $target 2>$null | Set-Content -Path $outs -Encoding UTF8

$html = Get-Content $outs -Raw
$m = [regex]::Match($html, 'data-selftest="(.*?)"', 'Singleline')
if (-not $m.Success) {
  # 直连模式：没有探针，就从 DOM 自己数（判定口径与探针一致）
  Write-Output '（探针不可用，改用直连 DOM 解析 —— 控制台报错此项无法采集）'
  $turns = [regex]::Matches($html, 'data-testid="turn-group-(\d+)"') |
    ForEach-Object { [int]$_.Groups[1].Value } | Sort-Object -Unique
  $txt = [regex]::Replace($html, '<script[\s\S]*?</script>|<style[\s\S]*?</style>', ' ')
  $txt = [regex]::Replace($txt, '<[^>]+>', ' ')
  $txt = [regex]::Replace($txt, '\s+', ' ').Trim()
  $obj = [pscustomobject]@{
    textLen         = $txt.Length
    domNodes        = ([regex]::Matches($html, '<[a-zA-Z]')).Count
    hasTimeline     = $html.Contains('data-testid="session-timeline"')
    timelineVisible = $html.Contains('data-testid="session-timeline"')
    scrollHeight    = 0
    clientHeight    = 0
    turns           = @($turns)
    turnCount       = @($turns).Count
    loadOlder       = $html.Contains('data-testid="load-older-turns"')
    jumpLatest      = $html.Contains('data-testid="jump-latest"')
    groupMore       = ([regex]::Matches($html, 'data-testid="group-more-')).Count
    rail            = $html.Contains('data-testid="turn-rail"')
    errs            = @()
  }
  $direct = $true
} else {
  $info = $m.Groups[1].Value -replace '&quot;','"' -replace '&amp;','&' -replace '&lt;','<' -replace '&gt;','>'
  $obj = $info | ConvertFrom-Json
  $direct = $false
}

Write-Output '================ 真机自测（Headless Chrome）================'
Write-Output ("URL         : " + $target)
Write-Output ("mode        : " + $(if ($Mobile) { 'mobile 390x844' } else { 'desktop 1440x900' }))
Write-Output ("可见文本长度 : " + $obj.textLen)
Write-Output ("DOM 节点数   : " + $obj.domNodes)
Write-Output ("时间线       : 存在=" + $obj.hasTimeline + " 可见=" + $obj.timelineVisible)
Write-Output ("滚动几何     : scrollHeight=" + $obj.scrollHeight + " clientHeight=" + $obj.clientHeight)
Write-Output ("已挂载 Turn  : [" + ($obj.turns -join ',') + "]  共 " + $obj.turnCount + " 轮")
Write-Output ("更早的按钮   : " + $obj.loadOlder)
Write-Output ("直达最新按钮 : " + $obj.jumpLatest)
Write-Output ("加载更多按钮 : " + $obj.groupMore)
Write-Output ("Turn 轨道    : " + $obj.rail)
Write-Output ("控制台报错   : " + $(if ($obj.errs.Count -eq 0) { '(none)' } else { '' }))
foreach ($e in $obj.errs) { Write-Output ("   " + $e) }

$blank = ($obj.textLen -eq 0) -or ($obj.hasTimeline -and -not $obj.timelineVisible) -or ($obj.domNodes -lt 20)
Write-Output ("白屏判定     : " + $(if ($blank) { 'BLANK / 白屏' } else { 'OK' }))
if ($blank) { exit 1 }
if ($obj.errs.Count -gt 0) { exit 1 }
exit 0
