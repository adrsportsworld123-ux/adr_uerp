# Local (non-Docker) Postgres helper: applies pending migrations/*.sql to the
# database described in .env.local, recording each applied file in
# ops.schema_migrations so re-running only applies new files.
#
#   powershell -File scripts/local-db.ps1            # apply pending migrations
#   powershell -File scripts/local-db.ps1 -Status    # list applied/pending
#
# Migrations run as the admin role (PGADMIN_USER - the schema owner), never
# as erp_app: the API must connect as the non-owner, NOBYPASSRLS erp_app role
# or every RLS policy is silently bypassed (see migrations/004).
# ops.schema_migrations lives in its own schema so the
# ALTER DEFAULT PRIVILEGES grants on `public` never expose it to erp_app.
param([switch]$Status)

$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent $PSScriptRoot
. (Join-Path $PSScriptRoot 'load-env.ps1') (Join-Path $root '.env.local')

$psql = $env:PSQL
if (-not $psql) { $psql = 'C:\Program Files\PostgreSQL\16\bin\psql.exe' }
if (-not (Test-Path $psql)) { throw "psql not found at '$psql' - set PSQL in .env.local" }

$env:PGHOST = $env:PGADMIN_HOST
$env:PGPORT = $env:PGADMIN_PORT
$env:PGDATABASE = $env:PGADMIN_DATABASE
$env:PGUSER = $env:PGADMIN_USER
$env:PGPASSWORD = $env:PGADMIN_PASSWORD
$env:PGOPTIONS = '-c client_min_messages=warning'

function Invoke-Psql([string[]]$psqlArgs) {
    $out = & $psql -X -v ON_ERROR_STOP=1 -q @psqlArgs
    if ($LASTEXITCODE -ne 0) { throw "psql failed (exit $LASTEXITCODE)" }
    return $out
}

Invoke-Psql @('-c', @'
CREATE SCHEMA IF NOT EXISTS ops;
CREATE TABLE IF NOT EXISTS ops.schema_migrations (
    filename   text PRIMARY KEY,
    applied_at timestamptz NOT NULL DEFAULT now()
);
'@) | Out-Null

$applied = @(Invoke-Psql @('-At', '-c', 'SELECT filename FROM ops.schema_migrations'))
$files = Get-ChildItem (Join-Path $root 'migrations') -Filter '*.sql' | Sort-Object Name
$pending = @($files | Where-Object { $applied -notcontains $_.Name })

if ($Status) {
    foreach ($f in $files) {
        $mark = if ($applied -contains $f.Name) { 'applied' } else { 'PENDING' }
        '{0,-8} {1}' -f $mark, $f.Name
    }
    return
}

if ($pending.Count -eq 0) { 'Database is up to date.'; return }

foreach ($f in $pending) {
    "Applying $($f.Name) ..."
    # One transaction per file: the migration and its bookkeeping row commit
    # together or not at all.
    $tmp = New-TemporaryFile
    try {
        $sql = Get-Content -Raw -Encoding UTF8 $f.FullName
        $name = $f.Name.Replace("'", "''")
        $body = $sql + "`n;`nINSERT INTO ops.schema_migrations (filename) VALUES ('$name');`n"
        [IO.File]::WriteAllText($tmp.FullName, $body, (New-Object Text.UTF8Encoding $false))
        Invoke-Psql @('--single-transaction', '-f', $tmp.FullName) | Out-Null
    } finally {
        Remove-Item $tmp.FullName -ErrorAction SilentlyContinue
    }
}
"Applied $($pending.Count) migration(s)."
