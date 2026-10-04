# Dot-source with a path to load KEY=VALUE lines into the process env.
# Blank lines and lines starting with # are ignored; values are taken
# literally (no quoting/expansion), so passwords with #, $ etc. are safe.
param([Parameter(Mandatory)][string]$Path)

if (-not (Test-Path $Path)) {
    throw "$Path not found - copy .env.local.example to .env.local and fill it in."
}
foreach ($line in Get-Content -Encoding UTF8 $Path) {
    $t = $line.Trim()
    if ($t -eq '' -or $t.StartsWith('#')) { continue }
    $i = $t.IndexOf('=')
    if ($i -lt 1) { continue }
    [Environment]::SetEnvironmentVariable($t.Substring(0, $i).Trim(), $t.Substring($i + 1), 'Process')
}
