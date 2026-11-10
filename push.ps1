$ErrorActionPreference = "Stop"
$CommitMessage = '❄️'
$Branch = 'master'
$Remotes = @('gitcrn')

git add .
git diff --cached --quiet
if ($LASTEXITCODE -eq 0) {
  Write-Host "Nema izmena za commit. Preskacem commit."
} else {
  git commit -m $CommitMessage
}

foreach ($remote in $Remotes) {
  if ([string]::IsNullOrWhiteSpace($Branch)) {
    git push $remote
  } else {
    git push $remote $Branch
  }
}
