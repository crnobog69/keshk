<#
.SYNOPSIS
    Брише све податке из Keshk базе података (ресетовање на чисто почетно стање).
#>
[CmdletBinding()]
param()

$ErrorActionPreference = 'Stop'
Set-Location -Path $PSScriptRoot

# 1. Заустави сервер ако тренутно ради
$running = Get-Process keshk -ErrorAction SilentlyContinue
if ($running) {
    Write-Host '[STOP] Заустављам Keshk процес...' -ForegroundColor Yellow
    $running | Stop-Process -Force -ErrorAction SilentlyContinue
    $running | Wait-Process -Timeout 3 -ErrorAction SilentlyContinue
    Start-Sleep -Milliseconds 400
}

# 2. Обриши SQLite фајлове (базу, WAL дневник и shared-memory)
$dbFiles = Get-ChildItem -Path 'data' -Filter 'keshk.db*' -File -ErrorAction SilentlyContinue
if ($dbFiles) {
    foreach ($f in $dbFiles) {
        Remove-Item -Path $f.FullName -Force
        Write-Host ('   - ' + $f.Name) -ForegroundColor DarkGray
    }
    Write-Host '[OK] Подаци из базе су обрисани.' -ForegroundColor Green
} else {
    Write-Host '[INFO] База је већ празна (нема фајлова у data/).' -ForegroundColor Cyan
}

if (-not (Test-Path 'data')) {
    New-Item -ItemType Directory -Path 'data' | Out-Null
}

Write-Host '[OK] База је успешно ресетована на почетно чисто стање!' -ForegroundColor Green
Write-Host '[START] Покрени сервер са: .\run.ps1' -ForegroundColor Cyan
