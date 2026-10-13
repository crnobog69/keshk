<#
.SYNOPSIS
    Keshk — скрипта за компајлирање и покретање на Windows-у или крос-компајлирање за Raspberry Pi 5.
.PARAMETER Target
    "local" (подразумевано) — компајлира и покреће keshk.exe на тренутном систему.
    "rpi5" — крос-компајлира 64-битни бинарни фајл за Linux ARM64 (Raspberry Pi 5).
#>
[CmdletBinding()]
param(
    [ValidateSet('local', 'rpi5')]
    [string]$Target = 'local'
)

$ErrorActionPreference = 'Stop'
Set-Location -Path $PSScriptRoot

Write-Host "[INFO] Keshk — покретање..." -ForegroundColor Cyan

# 1. Провера Go инсталације
if (-not (Get-Command go -ErrorAction SilentlyContinue)) {
    Write-Error "Go компајлер није инсталиран или није у PATH-у! Инсталирај Go са https://go.dev"
}

# 2. Припрема .env фајла ако не постоји
if (-not (Test-Path ".env")) {
    Write-Host "[INFO] Креирам .env из .env.example..." -ForegroundColor Yellow
    if (Test-Path ".env.example") {
        $secret = -join ((1..32) | ForEach-Object { '{0:x2}' -f (Get-Random -Maximum 256) })
        (Get-Content ".env.example" -Raw) `
            -replace 'SESSION_SECRET=', "SESSION_SECRET=$secret" `
            -replace 'KESHK_PASSWORD=промени-ме', 'KESHK_PASSWORD=keshk' |
            Set-Content ".env" -NoNewline
        Write-Host "[OK] Креиран .env са генерисаним SESSION_SECRET (лозинка: keshk)" -ForegroundColor Green
    } else {
        Write-Error "Недостаје .env.example фајл!"
    }
}

# 3. Крос-компајлирање за Raspberry Pi 5 (Linux arm64)
if ($Target -eq 'rpi5') {
    Write-Host "[BUILD] Компајлирам за Raspberry Pi 5 (Linux ARM64)..." -ForegroundColor Magenta
    $env:GOOS = "linux"
    $env:GOARCH = "arm64"
    $env:CGO_ENABLED = "0"
    try {
        go build -ldflags="-s -w" -o keshk-rpi5-arm64 .
        Write-Host "[OK] Успешно направљен: keshk-rpi5-arm64" -ForegroundColor Green
        Write-Host "[INFO] Пребаци га на RPi5 помоћу: scp keshk-rpi5-arm64 .env pi@raspberrypi:~/" -ForegroundColor Cyan
    } finally {
        $env:GOOS = ""
        $env:GOARCH = ""
        $env:CGO_ENABLED = ""
    }
    exit 0
}

# 4. Локално компајлирање и покретање (Windows)
$running = Get-Process keshk -ErrorAction SilentlyContinue
if ($running) {
    Write-Host "[STOP] Заустављам претходно покренут Keshk процес..." -ForegroundColor DarkGray
    $running | Stop-Process -Force -ErrorAction SilentlyContinue
    $running | Wait-Process -Timeout 3 -ErrorAction SilentlyContinue
    Start-Sleep -Milliseconds 300
}

Write-Host "[BUILD] Компајлирам keshk.exe..." -ForegroundColor Yellow
go build -o keshk.exe .
Write-Host "[START] Покрећем Keshk сервер..." -ForegroundColor Green
Write-Host "[INFO] Отвори у прегледачу: http://localhost:3000" -ForegroundColor Cyan
& ".\keshk.exe"
