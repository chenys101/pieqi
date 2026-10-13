# 真机验证：WS 断线重连后是否触发详情补拉（修复 1 + 修复 2 的行为验证）
#
# 判据（不依赖被测对象自述）：
#   1. 注入 hook 记录 /api/tasks/<id> 详情请求次数（应用启动时会拉 1 次）
#   2. 从页面内部 close() 掉 WebSocket —— 模拟"连接中断"（不触发 manualClose）
#   3. 等客户端自动重连（退避 1s 起）
#   4. 重连成功后，详情请求次数必须**增加** ⇒ 补拉真的发生了
#
# 为什么必须用 addScriptToEvaluateOnNewDocument：应用在页面加载时立刻 new WebSocket，
# 加载完再注入 hook 就抓不到那个实例了（上一轮实测已确认这个坑）。

param(
  [string]$Url = "http://127.0.0.1:3100",
  [string]$TaskId = "cedb5002-8319-426e-bf98-a135c564ea0e",
  [int]$Port = 9336
)

$ErrorActionPreference = 'Stop'
$chrome = "C:\Program Files\Google\Chrome\Application\chrome.exe"
if (-not (Test-Path $chrome)) { $chrome = "C:\Program Files (x86)\Google\Chrome\Application\chrome.exe" }

$prof = "$env:TEMP\cdp-reconnect-$(Get-Random)"
$proc = Start-Process -FilePath $chrome -ArgumentList @(
  "--headless=new", "--disable-gpu", "--remote-debugging-port=$Port",
  "--user-data-dir=$prof", "--window-size=390,844", "about:blank"
) -PassThru
Start-Sleep -Seconds 5

try {
  $targets = Invoke-RestMethod "http://127.0.0.1:$Port/json"
  $page = $targets | Where-Object { $_.type -eq 'page' } | Select-Object -First 1

  Add-Type -AssemblyName System.Net.WebSockets.Client
  $ws = New-Object System.Net.WebSockets.ClientWebSocket
  # ⚠️ 不要设 Origin：Chrome DevTools 端点会做 Origin 校验，显式设置会被判 403
  # （实测：期望 101 得到 403）。.NET 默认不带该头，Chrome 放行。
  $ct = [System.Threading.CancellationToken]::None
  $ws.ConnectAsync([Uri]$page.webSocketDebuggerUrl, $ct).Wait(8000) | Out-Null

  $script:msgId = 0
  function Send-Cdp([string]$method, $params) {
    $script:msgId++
    $wantId = $script:msgId
    $payload = @{ id = $wantId; method = $method; params = $params } | ConvertTo-Json -Depth 10 -Compress
    $bytes = [System.Text.Encoding]::UTF8.GetBytes($payload)
    $ws.SendAsync((New-Object System.ArraySegment[byte] -ArgumentList @(, $bytes)),
      [System.Net.WebSockets.WebSocketMessageType]::Text, $true, $ct).Wait(5000) | Out-Null

    # ⚠️ 必须按 id 匹配：CDP 同一连接上还会推事件（如 Page.* 通知），
    # 读到的第一条未必是本次响应。不匹配就会把事件当成结果、拿到空值
    # （上一版所有取值都是空字符串，就是这个原因）。
    $deadline = (Get-Date).AddSeconds(20)
    while ((Get-Date) -lt $deadline) {
      $buf = New-Object byte[] 1048576
      $seg = New-Object System.ArraySegment[byte] -ArgumentList @(, $buf)
      $sb = New-Object System.Text.StringBuilder
      do {
        $r = $ws.ReceiveAsync($seg, $ct)
        if (-not $r.Wait(15000)) { break }
        [void]$sb.Append([System.Text.Encoding]::UTF8.GetString($buf, 0, $r.Result.Count))
      } while (-not $r.Result.EndOfMessage)

      $obj = $sb.ToString() | ConvertFrom-Json
      if ($obj.id -eq $wantId) { return $obj }
      # 否则是别的通知，继续读
    }
    return $null
  }

  function Eval-Js([string]$expr) {
    $resp = Send-Cdp 'Runtime.evaluate' @{ expression = $expr; returnByValue = $true; awaitPromise = $true }
    if ($null -eq $resp) { return 'NO-RESPONSE' }
    if ($resp.result.exceptionDetails) { return "EXC: " + $resp.result.exceptionDetails.text }
    return $resp.result.result.value
  }

  # 关键：新文档创建时就注入（早于应用的 new WebSocket）
  $hook = @"
(function(){
  window.__probe = { detailFetches: 0, wsCreated: 0, wsClosed: 0, wsOpened: 0 };
  var of = window.fetch;
  window.fetch = function(u, o){
    try {
      var s = (typeof u === 'string') ? u : (u && u.url) || '';
      // 详情接口 = /api/tasks/<id>（不带 /cancel 之类的后缀）
      if (/\/api\/tasks\/[0-9a-f-]{36}(\?|`$)/.test(s)) window.__probe.detailFetches++;
    } catch(e){}
    return of.apply(this, arguments);
  };
  var Orig = window.WebSocket;
  function Patched(url, protos){
    var inst = protos ? new Orig(url, protos) : new Orig(url);
    window.__probe.wsCreated++;
    window.__wsLast = inst;
    inst.addEventListener('open', function(){ window.__probe.wsOpened++; });
    inst.addEventListener('close', function(){ window.__probe.wsClosed++; });
    return inst;
  }
  Patched.prototype = Orig.prototype;
  ['CONNECTING','OPEN','CLOSING','CLOSED'].forEach(function(k){ Patched[k] = Orig[k]; });
  window.WebSocket = Patched;
  return 'hooked';
})()
"@
  Send-Cdp 'Page.addScriptToEvaluateOnNewDocument' @{ source = $hook } | Out-Null
  Send-Cdp 'Page.enable' @{} | Out-Null
  Send-Cdp 'Page.navigate' @{ url = "$Url/sessions/$TaskId" } | Out-Null
  Start-Sleep -Seconds 8

  $snap1 = Eval-Js 'JSON.stringify(window.__probe)'
  Write-Output "STEP1 初次加载: $snap1"
  $f1 = (Eval-Js 'window.__probe.detailFetches')
  Write-Output "STEP1 详情请求次数 = $f1（应用启动自拉的 1 次）"

  # 模拟连接中断：从页面内部 close（不设 manualClose ⇒ 客户端应自动重连）
  $killed = Eval-Js @'
(function(){
  if (!window.__wsLast) return 'no-ws';
  try { window.__wsLast.close(); return 'closed'; } catch(e){ return 'err:' + e.message; }
})()
'@
  Write-Output "STEP2 已强制断开底层连接: $killed"

  # 等重连（退避首档 1s）+ 补拉完成
  Start-Sleep -Seconds 7

  $snap2 = Eval-Js 'JSON.stringify(window.__probe)'
  Write-Output "STEP3 重连后: $snap2"
  $f2 = (Eval-Js 'window.__probe.detailFetches')
  Write-Output "STEP3 详情请求次数 = $f2"

  Write-Output ""
  if ([int]$f2 -gt [int]$f1) {
    Write-Output "PASS: 重连触发了详情补拉（$f1 -> $f2）—— 修复 1/2 生效"
  } else {
    Write-Output "FAIL: 重连后未补拉（$f1 -> $f2）—— 详情仍会停在过期视图"
  }

  $opened = (Eval-Js 'window.__probe.wsOpened')
  Write-Output "（参考）WebSocket open 次数 = $opened，close 次数 = $(Eval-Js 'window.__probe.wsClosed')"

  $ws.Dispose()
}
finally {
  Stop-Process -Id $proc.Id -Force -ErrorAction SilentlyContinue
  Remove-Item $prof -Recurse -Force -ErrorAction SilentlyContinue
}
