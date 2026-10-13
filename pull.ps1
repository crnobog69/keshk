$ErrorActionPreference = "Stop"
$Branch = 'master'
$Remotes = @('gitcrn')

foreach ($remote in $Remotes) {
  if ([string]::IsNullOrWhiteSpace($Branch)) {
    git pull $remote
  } else {
    git pull $remote $Branch
  }
}
