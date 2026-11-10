<#
.SYNOPSIS
    Сачува измене (commit са српским датумом и временом: ДД.ММ.ГГГГ. ЧЧ:ММ:СС),
    направи таг ДД.ММ.ГГГГ_ЧЧ-ММ-СС и пошаље грану и таг И на gitcrn И на github.
.EXAMPLE
    .\docker-build.ps1
    .\docker-build.ps1 "Опис измене"
    .\docker-build.ps1 -Local
#>
[CmdletBinding()]
param(
    [Parameter(Position = 0)]
    [string]$Message = '',
    [string]$Branch = 'master',
    [switch]$Local
)

$ErrorActionPreference = 'Stop'
Set-Location -Path $PSScriptRoot

# 1. Тренутни датум и време (српски формат: ДД.ММ.ГГГГ.)
$now = Get-Date
$srDateTime = $now.ToString('dd.MM.yyyy. HH:mm:ss')
$tag = $now.ToString('dd.MM.yyyy_HH-mm-ss')

if ([string]::IsNullOrWhiteSpace($Message)) {
    $Message = $srDateTime
}

# 2. Опциони локални Docker билд (-Local)
if ($Local) {
    Write-Host "[BUILD] Локална изградња Docker слике keshk:$tag ..." -ForegroundColor Cyan
    docker build -t "keshk:$tag" -t "keshk:latest" .
    Write-Host "[OK] Изграђене локалне слике: keshk:$tag и keshk:latest" -ForegroundColor Green
    exit 0
}

# 3. Commit измена ако их има (порука = српски датум и време)
git add .
git diff --cached --quiet
if ($LASTEXITCODE -eq 0) {
    Write-Host "[GIT] Нема нових измена за commit — користим тренутни HEAD." -ForegroundColor DarkGray
} else {
    git commit -m $Message
    Write-Host "[COMMIT] $Message" -ForegroundColor Green
}

# 4. Креирање локалног Git тага
git tag $tag
Write-Host "[TAG] Креирана ознака: $tag" -ForegroundColor Green

# 5. Проналажење gitcrn и github remote-ова
$remoteMap = @{}
foreach ($line in (git remote -v)) {
    if ($line -match '^(\S+)\s+(\S+)\s+\(push\)') {
        $remoteMap[$Matches[1]] = $Matches[2]
    }
}

$crnRemote = $null
if ($remoteMap.ContainsKey('gitcrn')) {
    $crnRemote = 'gitcrn'
} else {
    foreach ($kv in $remoteMap.GetEnumerator()) {
        if ($kv.Value -match 'gitcrn|crnbg\.org') {
            $crnRemote = $kv.Key
            break
        }
    }
}

$ghRemote = $null
if ($remoteMap.ContainsKey('github')) {
    $ghRemote = 'github'
} else {
    foreach ($kv in $remoteMap.GetEnumerator()) {
        if ($kv.Value -match 'github\.com') {
            $ghRemote = $kv.Key
            break
        }
    }
}

# 6. Push гране и тага на gitcrn
if ($crnRemote) {
    Write-Host "[PUSH] Шаљем '$Branch' и '$tag' на gitcrn ($crnRemote)..." -ForegroundColor Cyan
    git push $crnRemote $Branch $tag
} else {
    Write-Warning "Није пронађен gitcrn remote — прескачем слање на gitcrn."
}

# 7. Push гране и тага на GitHub (покреће GitHub Actions Docker билд)
if ($ghRemote) {
    Write-Host "[PUSH] Шаљем '$Branch' и '$tag' на GitHub ($ghRemote)..." -ForegroundColor Cyan
    git push $ghRemote $Branch $tag
    Write-Host "[OK] Покренут је GitHub Actions билд за ознаке :$tag и :latest" -ForegroundColor Green
    Write-Host "[INFO] За праћење изградње уживо укуцај: gh run watch" -ForegroundColor Yellow
} else {
    Write-Error "Није пронађен 'github' remote! Додај га једном са:`n  gh repo create keshk --private --source=. --remote=github"
}
