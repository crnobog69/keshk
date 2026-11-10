# Generiše 64 nasumična hex karaktera (32 bajta) iz kriptografski sigurnog izvora.
# Radi u Windows PowerShell 5.1 i PowerShell 7+.

$bytes = New-Object byte[] 32
$rng = [System.Security.Cryptography.RandomNumberGenerator]::Create()
try {
    $rng.GetBytes($bytes)
}
finally {
    $rng.Dispose()
}

($bytes | ForEach-Object { $_.ToString('x2') }) -join ''