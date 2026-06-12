# Smoke driver for remnaWake: builds the service, launches it fully offline
# (fake Telegram token, unreachable panel), and drives the Mini App HTTP API
# with self-signed Telegram initData. Exits 0 on PASS, 1 on FAIL.
#
# Usage:  powershell -File .claude\skills\run-remnawake\smoke.ps1 [-Port 18086] [-KeepRunning]
#
# -KeepRunning leaves the server up (prints the PID and a ready Authorization
# header) so you can poke /api/* manually; otherwise the server is stopped.

param(
    [int]$Port = 18086,
    [switch]$KeepRunning
)

$ErrorActionPreference = 'Stop'
$repo = Split-Path -Parent (Split-Path -Parent (Split-Path -Parent $PSScriptRoot))
Set-Location $repo

$botToken = '123456:SMOKE-TEST-TOKEN'
$adminID  = 42
$base     = "http://127.0.0.1:$Port"
$logFile  = Join-Path $env:TEMP 'remnawake-smoke.log'
$dbFile   = Join-Path $env:TEMP ("remnawake-smoke-{0}.db" -f ([guid]::NewGuid().ToString('N').Substring(0, 8)))

function Fail($msg) { Write-Host "FAIL: $msg" -ForegroundColor Red; exit 1 }

# --- build ---
Write-Host "building..."
go build -o bin\remnawake-smoke.exe .
if ($LASTEXITCODE -ne 0) { Fail "go build failed" }

# --- sign Telegram initData (per https://core.telegram.org/bots/webapps#validating-data-received) ---
function New-InitData([long]$userID) {
    $authDate = [DateTimeOffset]::UtcNow.ToUnixTimeSeconds()
    $user = "{`"id`":$userID}"
    # data-check-string: key=value pairs sorted by key, joined with \n, hash excluded
    $pairs = @("auth_date=$authDate", "user=$user") | Sort-Object
    $dataCheck = $pairs -join "`n"
    $hmac1 = [System.Security.Cryptography.HMACSHA256]::new([Text.Encoding]::UTF8.GetBytes('WebAppData'))
    $secret = $hmac1.ComputeHash([Text.Encoding]::UTF8.GetBytes($botToken))
    $hmac2 = [System.Security.Cryptography.HMACSHA256]::new($secret)
    $hash = ($hmac2.ComputeHash([Text.Encoding]::UTF8.GetBytes($dataCheck)) | ForEach-Object { $_.ToString('x2') }) -join ''
    "auth_date=$authDate&user=$([uri]::EscapeDataString($user))&hash=$hash"
}

# --- launch ---
$envBlock = @{
    REMNAWAVE_BASE_URL  = 'http://127.0.0.1:9'   # unreachable on purpose: panel calls fail fast
    REMNAWAVE_API_TOKEN = 'smoke-dummy'
    TELEGRAM_BOT_TOKEN  = $botToken
    TELEGRAM_ADMIN_ID   = "$adminID"
    WEBAPP_URL          = 'https://smoke.invalid' # must be https; only enables the server, never fetched
    WEBAPP_LISTEN       = "127.0.0.1:$Port"
    DB_PATH             = $dbFile
    DRY_RUN             = 'true'
    RUN_ON_START        = 'false'                 # skip the startup notify sweep against the dead panel
    LOG_LEVEL           = 'info'
}
foreach ($k in $envBlock.Keys) { Set-Item "env:$k" $envBlock[$k] }

Write-Host "starting server on $base (log: $logFile)..."
$proc = Start-Process -FilePath (Join-Path $repo 'bin\remnawake-smoke.exe') `
    -RedirectStandardOutput $logFile -RedirectStandardError "$logFile.err" `
    -PassThru -WindowStyle Hidden

$stopServer = { if (-not $KeepRunning -and $proc -and -not $proc.HasExited) { Stop-Process -Id $proc.Id -Force -Confirm:$false } }

try {
    # --- wait for the static frontend ---
    $ready = $false
    foreach ($i in 1..30) {
        try {
            $r = Invoke-WebRequest -UseBasicParsing -Uri "$base/" -TimeoutSec 2
            if ($r.StatusCode -eq 200 -and $r.Content -match '<html') { $ready = $true; break }
        } catch { Start-Sleep -Milliseconds 500 }
    }
    if (-not $ready) { & $stopServer; Get-Content $logFile -Tail 20; Fail "server did not come up on $base" }
    Write-Host "  OK  GET / serves the embedded frontend"

    $auth = @{ Authorization = "tma $(New-InitData $adminID)" }

    # --- unauthenticated request is rejected ---
    try {
        Invoke-RestMethod -Uri "$base/api/admin" -TimeoutSec 5 | Out-Null
        & $stopServer; Fail "/api/admin without initData should be 401"
    } catch {
        if ($_.Exception.Response.StatusCode.value__ -ne 401) { & $stopServer; Fail "expected 401, got $($_.Exception.Response.StatusCode.value__)" }
    }
    Write-Host "  OK  GET /api/admin without auth -> 401"

    # --- admin panel (SQLite only, works without the panel) ---
    $panel = Invoke-RestMethod -Uri "$base/api/admin" -Headers $auth -TimeoutSec 5
    if ($panel.require_screenshot -ne $false) { & $stopServer; Fail "fresh DB should report require_screenshot=false" }
    Write-Host "  OK  GET /api/admin -> 200 (require_screenshot=false)"

    # --- mutate: add a tariff, flip the screenshot toggle ---
    Invoke-RestMethod -Uri "$base/api/admin/tariff" -Headers $auth -Method Post -ContentType 'application/json' `
        -Body '{"months":3,"price":450}' -TimeoutSec 5 | Out-Null
    Invoke-RestMethod -Uri "$base/api/admin/screenshot-toggle" -Headers $auth -Method Post -ContentType 'application/json' `
        -Body '{"enabled":true}' -TimeoutSec 5 | Out-Null
    $panel = Invoke-RestMethod -Uri "$base/api/admin" -Headers $auth -TimeoutSec 5
    if ($panel.require_screenshot -ne $true) { & $stopServer; Fail "screenshot toggle did not persist" }
    if ($panel.tariffs[0].months -ne 3 -or $panel.tariffs[0].price -ne 450) { & $stopServer; Fail "tariff not saved: $($panel.tariffs | ConvertTo-Json)" }
    Write-Host "  OK  POST tariff + screenshot-toggle -> reflected in panel JSON"

    # --- /api/me needs the panel; 502 here proves auth passed and the panel call was attempted ---
    try {
        Invoke-RestMethod -Uri "$base/api/me" -Headers $auth -TimeoutSec 10 | Out-Null
        & $stopServer; Fail "/api/me should fail without a reachable panel"
    } catch {
        if ($_.Exception.Response.StatusCode.value__ -ne 502) { & $stopServer; Fail "expected 502 from /api/me, got $($_.Exception.Response.StatusCode.value__)" }
    }
    Write-Host "  OK  GET /api/me -> 502 (panel unreachable, auth accepted)"
} finally {
    & $stopServer
}

if ($KeepRunning) {
    Write-Host ""
    Write-Host "server left running: PID $($proc.Id) on $base"
    Write-Host "Authorization header: tma $(New-InitData $adminID)"
    Write-Host "stop with: Stop-Process -Id $($proc.Id) -Force"
} else {
    Remove-Item $dbFile -ErrorAction SilentlyContinue
}
Write-Host ""
Write-Host "PASS" -ForegroundColor Green
exit 0
