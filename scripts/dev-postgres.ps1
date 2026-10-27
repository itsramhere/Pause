param([ValidateSet('start','stop','status')][string]$Action='start', [string]$PgBin='C:\Program Files\PostgreSQL\18\bin', [int]$Port=55432)
$ErrorActionPreference='Stop'
$taskRoot=[IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
$taskData=Join-Path $taskRoot 'data/postgres'
$taskLog=Join-Path $taskRoot 'data/postgres.log'
if (-not (Test-Path -LiteralPath (Join-Path $PgBin 'pg_ctl.exe'))) { throw 'Set -PgBin to your PostgreSQL bin directory.' }
if ($Action -eq 'stop') { & (Join-Path $PgBin 'pg_ctl.exe') -D $taskData -m fast -w stop; exit $LASTEXITCODE }
if ($Action -eq 'status') { & (Join-Path $PgBin 'pg_ctl.exe') -D $taskData status; exit $LASTEXITCODE }
if (-not (Test-Path -LiteralPath (Join-Path $taskData 'PG_VERSION'))) {
 New-Item -ItemType Directory -Force -Path $taskData | Out-Null
 # This disposable developer cluster trusts local loopback connections only.
 # Hosted/native shared installations must use their own authenticated DSN.
 & (Join-Path $PgBin 'initdb.exe') -D $taskData -U pause --auth-local=trust --auth-host=trust --encoding=UTF8 --locale=C
 if ($LASTEXITCODE -ne 0) { throw 'initdb failed' }
}
& (Join-Path $PgBin 'pg_ctl.exe') -D $taskData status *> $null
if ($LASTEXITCODE -ne 0) {
 & (Join-Path $PgBin 'pg_ctl.exe') -D $taskData -l $taskLog -o "-h 127.0.0.1 -p $Port" -w start
 if ($LASTEXITCODE -ne 0) { throw 'Postgres startup failed' }
}
$taskExists=& (Join-Path $PgBin 'psql.exe') -h 127.0.0.1 -p $Port -U pause -d postgres -tAc "SELECT 1 FROM pg_database WHERE datname='pause'"
if ($LASTEXITCODE -ne 0) { throw 'Postgres connection failed' }
if ($taskExists -ne '1') { & (Join-Path $PgBin 'createdb.exe') -h 127.0.0.1 -p $Port -U pause pause; if ($LASTEXITCODE -ne 0) { throw 'Database creation failed' } }
Write-Output "Local database ready: postgres://pause@127.0.0.1:$Port/pause?sslmode=disable"
