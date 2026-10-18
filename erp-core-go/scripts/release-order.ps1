# Releases a stuck, unpaid sales order (an open cart or held order) on the
# database described in .env.local - see release-order.sql for exactly
# what it does. Dry run by default (shows the result, then rolls back).
#
#   powershell -ExecutionPolicy Bypass -File scripts/release-order.ps1 -OrderId <uuid> -Reason "Abandoned cart"
#   powershell -ExecutionPolicy Bypass -File scripts/release-order.ps1 -OrderId <uuid> -Reason "Abandoned cart" -By admin@adrsw.com -Commit
param(
    [Parameter(Mandatory = $true)][string]$OrderId,
    [Parameter(Mandatory = $true)][string]$Reason,
    [string]$By = '',
    [switch]$Commit
)

$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent $PSScriptRoot
. (Join-Path $PSScriptRoot 'load-env.ps1') (Join-Path $root '.env.local')

$psql = $env:PSQL
if (-not $psql) { $psql = 'C:\Program Files\PostgreSQL\16\bin\psql.exe' }
if (-not (Test-Path $psql)) { throw "psql not found at '$psql' - set PSQL in .env.local" }
if ($OrderId -notmatch '^[0-9a-fA-F-]{36}$') { throw "OrderId must be a UUID" }

$env:PGHOST = $env:PGADMIN_HOST
$env:PGPORT = $env:PGADMIN_PORT
$env:PGDATABASE = $env:PGADMIN_DATABASE
$env:PGUSER = $env:PGADMIN_USER
$env:PGPASSWORD = $env:PGADMIN_PASSWORD

$commitFlag = if ($Commit) { 'yes' } else { 'no' }
& $psql -X -q -v "order_id=$OrderId" -v "reason=$Reason" -v "by_email=$By" -v "commit=$commitFlag" `
    -f (Join-Path $PSScriptRoot 'release-order.sql')
if ($LASTEXITCODE -ne 0) { throw "release failed (exit $LASTEXITCODE) - nothing was changed" }
