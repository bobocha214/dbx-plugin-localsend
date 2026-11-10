param(
    [Parameter(Mandatory = $true)][string]$Tag,
    [string]$RepoRoot = (Split-Path -Parent $PSScriptRoot)
)
$ErrorActionPreference = 'Stop'
if ($Tag -notmatch '^v(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(-[0-9A-Za-z]+(?:[.-][0-9A-Za-z]+)*)?$') {
    throw 'Tag must be vMAJOR.MINOR.PATCH, optionally with a prerelease suffix (for example v0.2.0-beta.1).'
}
$version = $Tag.Substring(1)
$manifest = Get-Content -LiteralPath (Join-Path $RepoRoot 'manifest.json') -Raw -Encoding utf8 | ConvertFrom-Json
if ($manifest.version -ne $version) { throw "Tag $Tag does not match manifest version $($manifest.version)." }
$backend = Get-Content -LiteralPath (Join-Path $RepoRoot 'backend/main.go') -Raw -Encoding utf8
if ($backend -notmatch ('pluginVersion\s*=\s*"' + [regex]::Escape($version) + '"')) { throw 'Backend version does not match tag.' }
$html = Get-Content -LiteralPath (Join-Path $RepoRoot 'ui/index.html') -Raw -Encoding utf8
if (!$html.Contains("邻间 $version</span>")) { throw 'UI version does not match tag.' }
$notes = Join-Path $RepoRoot "docs/RELEASE-$version.md"
if (!(Test-Path -LiteralPath $notes -PathType Leaf)) { throw "Missing release notes: docs/RELEASE-$version.md" }
$body = Get-Content -LiteralPath $notes -Raw -Encoding utf8
if ([string]::IsNullOrWhiteSpace(($body -replace '(?m)^#.*$', '').Trim())) { throw 'Release notes must contain update details, not just a heading.' }
Write-Output "Release validated: $Tag; notes: docs/RELEASE-$version.md"
