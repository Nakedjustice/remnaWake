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
$runID    = [guid]::NewGuid().ToString('N').Substring(0, 8)
$logFile  = Join-Path $env:TEMP "remnawake-smoke-$runID.log"
$dbFile   = Join-Path $env:TEMP "remnawake-smoke-$runID.db"

function Fail($msg) {
    Write-Host "FAIL: $msg" -ForegroundColor Red
    if ($logFile -and (Test-Path $logFile)) { Write-Host "log: $logFile" }
    exit 1
}

# Assert-HttpError expects the request to fail with a specific HTTP status.
# A transport-level failure (connection refused, timeout) has no Response —
# report the real exception instead of a misleading "got <empty>".
function Assert-HttpError([string]$Uri, $Headers, [int]$ExpectedStatus, [string]$Label) {
    try {
        Invoke-RestMethod -Uri $Uri -Headers $Headers -TimeoutSec 10 | Out-Null
        Fail "$Label should be $ExpectedStatus but the request succeeded"
    } catch {
        $resp = $_.Exception.Response
        if ($null -eq $resp) { Fail "$Label expected HTTP $ExpectedStatus but got no response: $($_.Exception.Message)" }
        $code = [int]$resp.StatusCode
        if ($code -ne $ExpectedStatus) { Fail "$Label expected $ExpectedStatus, got $code" }
    }
}

# --- preflight: a leftover -KeepRunning server keeps the exe image and the
# --- port locked, making 'go build' and the readiness check fail confusingly.
$stale = Get-Process remnawake-smoke -ErrorAction SilentlyContinue
if ($stale) {
    Fail "a previous smoke server is still running (PID $(($stale | ForEach-Object Id) -join ', ')); stop it with: Stop-Process -Name remnawake-smoke -Force"
}

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
Write-Host "server PID $($proc.Id)"

# All failure paths go through Fail -> exit 1; the finally below still runs
# and stops the server (unless -KeepRunning), so no inline cleanup is needed.
$stopServer = {
    if (-not $KeepRunning -and $proc -and -not $proc.HasExited) {
        Stop-Process -Id $proc.Id -Force -Confirm:$false
        # Wait for the handles on the DB and redirected logs to be released,
        # otherwise the artifact sweep below races the kill and leaves litter.
        $proc.WaitForExit(5000) | Out-Null
    }
}

try {
    # --- wait for the static frontend ---
    $ready = $false
    foreach ($i in 1..30) {
        if ($proc.HasExited) {
            Get-Content $logFile -Tail 20 -ErrorAction SilentlyContinue
            Get-Content "$logFile.err" -Tail 20 -ErrorAction SilentlyContinue
            Fail "server exited early (code $($proc.ExitCode))"
        }
        try {
            $r = Invoke-WebRequest -UseBasicParsing -Uri "$base/" -TimeoutSec 2
            if ($r.StatusCode -eq 200 -and $r.Content -match '<html') { $ready = $true; break }
        } catch {}
        Start-Sleep -Milliseconds 500
    }
    if (-not $ready) { Get-Content $logFile -Tail 20 -ErrorAction SilentlyContinue; Fail "server did not come up on $base" }
    Write-Host "  OK  GET / serves the embedded frontend"

    $auth = @{ Authorization = "tma $(New-InitData $adminID)" }

    # --- unauthenticated request is rejected ---
    Assert-HttpError "$base/api/admin" $null 401 "GET /api/admin without auth"
    Write-Host "  OK  GET /api/admin without auth -> 401"

    # --- admin panel (SQLite only, works without the panel) ---
    $panel = Invoke-RestMethod -Uri "$base/api/admin" -Headers $auth -TimeoutSec 5
    if ($panel.require_screenshot -ne $false) { Fail "fresh DB should report require_screenshot=false" }
    Write-Host "  OK  GET /api/admin -> 200 (require_screenshot=false)"

    # --- mutate: add a tariff, flip the screenshot toggle ---
    Invoke-RestMethod -Uri "$base/api/admin/tariff" -Headers $auth -Method Post -ContentType 'application/json' `
        -Body '{"months":3,"price":450}' -TimeoutSec 5 | Out-Null
    Invoke-RestMethod -Uri "$base/api/admin/screenshot-toggle" -Headers $auth -Method Post -ContentType 'application/json' `
        -Body '{"enabled":true}' -TimeoutSec 5 | Out-Null
    $panel = Invoke-RestMethod -Uri "$base/api/admin" -Headers $auth -TimeoutSec 5
    if ($panel.require_screenshot -ne $true) { Fail "screenshot toggle did not persist" }
    if ($panel.tariffs[0].months -ne 3 -or $panel.tariffs[0].price -ne 450) { Fail "tariff not saved: $($panel.tariffs | ConvertTo-Json)" }
    Write-Host "  OK  POST tariff + screenshot-toggle -> reflected in panel JSON"

    # --- /api/me needs the panel; 502 here proves auth passed and the panel call was attempted ---
    Assert-HttpError "$base/api/me" $auth 502 "GET /api/me without a reachable panel"
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
    # SQLite runs in WAL mode and the server was force-stopped, so the -wal/-shm
    # sidecars survive the .db alone; sweep the whole run's artifacts.
    Remove-Item "$dbFile*" -ErrorAction SilentlyContinue
    Remove-Item "$logFile*" -ErrorAction SilentlyContinue
}
Write-Host ""
Write-Host "PASS" -ForegroundColor Green
exit 0
