[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)]
    [ValidateNotNullOrEmpty()]
    [string] $Path,

    [Parameter(Mandatory = $true)]
    [ValidateNotNullOrEmpty()]
    [string] $Message
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

$conventionalCommitPattern = '^(feat|fix|docs|style|refactor|perf|test|build|ci|chore|revert)\([a-z0-9]+(?:-[a-z0-9]+)*(?:/[a-z0-9]+(?:-[a-z0-9]+)*)*\)!?: .+$'
if ($Message -cnotmatch $conventionalCommitPattern) {
    throw "Commit message must use a scoped Conventional Commit format: type(scope): specific description. Received: $Message"
}

$repoRoot = (& git rev-parse --show-toplevel).Trim()
if ($LASTEXITCODE -ne 0) {
    throw 'Run this script from inside the Git repository.'
}

$resolvedPath = [System.IO.Path]::GetFullPath((Join-Path (Get-Location).Path $Path))
$relativePath = [System.IO.Path]::GetRelativePath($repoRoot, $resolvedPath)
if ($relativePath.StartsWith('..', [System.StringComparison]::Ordinal) -or [System.IO.Path]::IsPathRooted($relativePath)) {
    throw "Refusing to commit a file outside the repository: $Path"
}
$gitPath = $relativePath.Replace('\', '/')
if (-not (Test-Path -LiteralPath $resolvedPath -PathType Leaf)) {
    throw "File does not exist: $Path"
}

$stagedPaths = @(& git diff --cached --name-only)
if ($LASTEXITCODE -ne 0) {
    throw 'Could not inspect the Git index.'
}
if ($stagedPaths.Count -gt 0) {
    throw "The Git index is already staged. Commit or unstage those paths before committing $relativePath."
}

& git add -- $gitPath
if ($LASTEXITCODE -ne 0) {
    throw "Could not stage $relativePath."
}

$stagedPaths = @(& git diff --cached --name-only)
if ($LASTEXITCODE -ne 0 -or $stagedPaths.Count -ne 1 -or $stagedPaths[0] -ne $gitPath) {
    & git reset -- $gitPath
    throw "Expected exactly one staged file ($gitPath); the index did not match."
}

& git commit -m $Message
if ($LASTEXITCODE -ne 0) {
    throw "Commit failed for $gitPath."
}
