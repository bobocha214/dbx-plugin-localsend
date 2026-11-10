param([string]$Version = '')
$ErrorActionPreference = 'Stop'
$repo = Split-Path -Parent $PSScriptRoot
$sourceManifest = Get-Content -LiteralPath (Join-Path $repo 'manifest.json') -Raw -Encoding utf8 | ConvertFrom-Json
if (!$Version) { $Version = $sourceManifest.version }
if ($Version -ne $sourceManifest.version) { throw 'Manifest version mismatch' }
if ($Version -notmatch '^\d+\.\d+\.\d+(-[0-9A-Za-z]+(?:[.-][0-9A-Za-z]+)*)?$') { throw 'Invalid package version' }
$output = Join-Path $repo 'dist'
New-Item -ItemType Directory -Force -Path $output | Out-Null
$stage = Join-Path $output ('im-stage-' + [Guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path (Join-Path $stage 'bin') -Force | Out-Null
Copy-Item -LiteralPath (Join-Path $repo 'manifest.json') -Destination $stage
Copy-Item -LiteralPath (Join-Path $repo 'assets') -Destination $stage -Recurse
New-Item -ItemType Directory -Path (Join-Path $stage 'ui') | Out-Null
foreach ($name in @('index.html','im.css','im.js','im-archive.js')) {
    Copy-Item -LiteralPath (Join-Path $repo "ui/$name") -Destination (Join-Path $stage 'ui')
}
Copy-Item -LiteralPath (Join-Path $repo 'ui/vendor') -Destination (Join-Path $stage 'ui') -Recurse
$manifest = Get-Content (Join-Path $stage 'manifest.json') -Raw | ConvertFrom-Json
if ($manifest.version -ne $Version) { throw 'Manifest version mismatch' }
$manifest.entrypoints.backend.executable = 'bin/dbx-plugin-localsend.exe'
$manifest | ConvertTo-Json -Depth 30 | Set-Content -Encoding utf8 (Join-Path $stage 'manifest.json')
Push-Location (Join-Path $repo 'backend')
try {
    & go build '-ldflags=-s -w' -o (Join-Path $stage 'bin/dbx-plugin-localsend.exe') .
    if ($LASTEXITCODE -ne 0) { throw 'Go build failed' }
} finally { Pop-Location }
$hashes = [ordered]@{}
Get-ChildItem -LiteralPath $stage -File -Recurse | ForEach-Object {
    $relative = $_.FullName.Substring($stage.Length + 1).Replace('\','/')
    $hashes[$relative] = (Get-FileHash -LiteralPath $_.FullName -Algorithm SHA256).Hash.ToLowerInvariant()
}
@{ algorithm='sha256'; files=$hashes } | ConvertTo-Json -Depth 10 | Set-Content -Encoding utf8 (Join-Path $stage 'checksums.json')
$zip = Join-Path $output "io.github.yuzhi.localsend-$Version-windows-x64.zip"
$package = Join-Path $output "io.github.yuzhi.localsend-$Version-windows-x64.dbxp"
Compress-Archive -Path (Join-Path $stage '*') -DestinationPath $zip -Force
Move-Item -LiteralPath $zip -Destination $package -Force
$packageHash = (Get-FileHash -LiteralPath $package -Algorithm SHA256).Hash.ToLowerInvariant()
"$packageHash  $([IO.Path]::GetFileName($package))" | Set-Content -LiteralPath ($package + '.sha256') -Encoding ascii
[ordered]@{sha256=$packageHash;size=(Get-Item -LiteralPath $package).Length;target='windows-x64';url=[IO.Path]::GetFileName($package)} | ConvertTo-Json | Set-Content -LiteralPath (Join-Path $output "io.github.yuzhi.localsend-$Version-windows-x64.artifact.json") -Encoding utf8
Get-FileHash -LiteralPath $package -Algorithm SHA256
