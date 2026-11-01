[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)]
    [ValidateNotNullOrEmpty()]
    [object[]] $Items
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

$repoRoot = (& git rev-parse --show-toplevel).Trim()
if ($LASTEXITCODE -ne 0) {
    throw 'Run this script from inside the Git repository.'
}
Set-Location -LiteralPath $repoRoot

$singleCommitScript = Join-Path $PSScriptRoot 'commit-one.ps1'
foreach ($item in $Items) {
    if (-not $item.Contains('Path') -or -not $item.Contains('Message')) {
        throw 'Each commit item must define Path and Message.'
    }
    & $singleCommitScript -Path ([string] $item.Path) -Message ([string] $item.Message)
    if ($LASTEXITCODE -ne 0) {
        throw "Commit series stopped after failure for $($item.Path)."
    }
}
