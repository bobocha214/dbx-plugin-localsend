param(
    [string]$Version = '',
    [ValidateSet('windows-x64','windows-arm64','macos-x64','macos-arm64','linux-x64','linux-arm64')]
    [string]$Target = 'windows-x64'
)
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
Copy-Item -LiteralPath (Join-Path $repo 'LICENSE') -Destination $stage
Copy-Item -LiteralPath (Join-Path $repo 'assets') -Destination $stage -Recurse
New-Item -ItemType Directory -Path (Join-Path $stage 'ui') | Out-Null
foreach ($name in @('index.html','im.css','im.js','im-archive.js')) {
    Copy-Item -LiteralPath (Join-Path $repo "ui/$name") -Destination (Join-Path $stage 'ui')
}
Copy-Item -LiteralPath (Join-Path $repo 'ui/vendor') -Destination (Join-Path $stage 'ui') -Recurse
$manifest = Get-Content (Join-Path $stage 'manifest.json') -Raw | ConvertFrom-Json
if ($manifest.version -ne $Version) { throw 'Manifest version mismatch' }
$platform, $architecture = $Target.Split('-')
$goOS = if ($platform -eq 'macos') { 'darwin' } else { $platform }
$goArchitecture = if ($architecture -eq 'x64') { 'amd64' } else { 'arm64' }
$binary = if ($platform -eq 'windows') { 'bin/dbx-plugin-localsend.exe' } else { 'bin/dbx-plugin-localsend' }
$manifest.entrypoints.backend.executable = $binary
$manifest | ConvertTo-Json -Depth 30 | Set-Content -Encoding utf8 (Join-Path $stage 'manifest.json')
Push-Location (Join-Path $repo 'backend')
$previousGoOS, $previousGoArch, $previousCGO = $env:GOOS, $env:GOARCH, $env:CGO_ENABLED
try {
    $env:GOOS = $goOS
    $env:GOARCH = $goArchitecture
    $env:CGO_ENABLED = '0'
    & go build '-ldflags=-s -w' -o (Join-Path $stage $binary) .
    if ($LASTEXITCODE -ne 0) { throw 'Go build failed' }
} finally {
    $env:GOOS, $env:GOARCH, $env:CGO_ENABLED = $previousGoOS, $previousGoArch, $previousCGO
    Pop-Location
}
$hashes = [ordered]@{}
Get-ChildItem -LiteralPath $stage -File -Recurse | ForEach-Object {
    $relative = $_.FullName.Substring($stage.Length + 1).Replace([IO.Path]::DirectorySeparatorChar, [char]'/')
    $hashes[$relative] = (Get-FileHash -LiteralPath $_.FullName -Algorithm SHA256).Hash.ToLowerInvariant()
}
@{ algorithm='sha256'; files=$hashes } | ConvertTo-Json -Depth 10 | Set-Content -Encoding utf8 (Join-Path $stage 'checksums.json')
$package = Join-Path $output "io.github.yuzhi.localsend-$Version-$Target.dbxp"
# Python writes the Unix creator marker as well as mode bits, so DBX's Go ZIP
# reader can restore executable permissions when the package was built on Windows.
& python (Join-Path $PSScriptRoot 'create-package.py') $stage $package
if ($LASTEXITCODE -ne 0) { throw 'Package archive creation failed' }
$packageHash = (Get-FileHash -LiteralPath $package -Algorithm SHA256).Hash.ToLowerInvariant()
"$packageHash  $([IO.Path]::GetFileName($package))" | Set-Content -LiteralPath ($package + '.sha256') -Encoding ascii
[ordered]@{sha256=$packageHash;size=(Get-Item -LiteralPath $package).Length;target=$Target;url=[IO.Path]::GetFileName($package)} | ConvertTo-Json | Set-Content -LiteralPath (Join-Path $output "io.github.yuzhi.localsend-$Version-$Target.artifact.json") -Encoding utf8
Get-FileHash -LiteralPath $package -Algorithm SHA256
