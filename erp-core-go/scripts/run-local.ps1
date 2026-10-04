# Runs the API against the local (non-Docker) Postgres configured in .env.local.
#   powershell -File scripts/run-local.ps1
$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent $PSScriptRoot
. (Join-Path $PSScriptRoot 'load-env.ps1') (Join-Path $root '.env.local')

Push-Location $root
try { go run ./cmd/api } finally { Pop-Location }
