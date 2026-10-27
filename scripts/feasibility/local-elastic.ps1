param([ValidateSet('start', 'stop', 'status')][string]$Action = 'status')
$ErrorActionPreference = 'Stop'
$taskRoot = (Resolve-Path -LiteralPath (Join-Path $PSScriptRoot '../..')).Path
$taskElasticRoot = Join-Path $taskRoot 'planning/evidence/P00/local-elastic'
$taskVersion = '9.5.5'
$taskElasticHome = Join-Path $taskElasticRoot "elasticsearch-$taskVersion"

if ($Action -eq 'status') {
    Invoke-RestMethod -Uri 'http://127.0.0.1:19200/' -TimeoutSec 5 |
        Select-Object name, cluster_name, cluster_uuid, version
    exit
}
if ($Action -eq 'stop') {
    # Only processes executing this task's downloaded JDK are eligible.
    $taskJavaPath = Join-Path $taskElasticHome 'jdk/bin/java.exe'
    Get-CimInstance Win32_Process -Filter "Name = 'java.exe'" |
        Where-Object { $_.ExecutablePath -and ([IO.Path]::GetFullPath($_.ExecutablePath) -ieq [IO.Path]::GetFullPath($taskJavaPath)) } |
        ForEach-Object { Stop-Process -Id $_.ProcessId -Force }
    Write-Output 'Stopped task-local Elastic Java processes.'
    exit
}

# A localhost-only disposable instance. This does not install a Windows service,
# change the user's .env, or provision a paid/cloud cluster.
New-Item -ItemType Directory -Path $taskElasticRoot -Force | Out-Null
$taskArchive = Join-Path $taskElasticRoot "elasticsearch-$taskVersion-windows-x86_64.zip"
$taskArchiveURL = "https://artifacts.elastic.co/downloads/elasticsearch/elasticsearch-$taskVersion-windows-x86_64.zip"
if (-not (Test-Path -LiteralPath $taskArchive)) {
    curl.exe --fail --silent --show-error --location --max-time 240 --output $taskArchive $taskArchiveURL
    if ($LASTEXITCODE -ne 0) { throw 'Elastic archive download failed' }
}
curl.exe --fail --silent --show-error --location --max-time 30 --output "$taskArchive.sha512" "$taskArchiveURL.sha512"
if ($LASTEXITCODE -ne 0) { throw 'Elastic checksum download failed' }
$taskExpectedHash = ((Get-Content -LiteralPath "$taskArchive.sha512" -Raw) -split '\s+')[0]
if ((Get-FileHash -LiteralPath $taskArchive -Algorithm SHA512).Hash -ine $taskExpectedHash) {
    throw 'Elastic archive checksum mismatch; preserve for investigation'
}
if (-not (Test-Path -LiteralPath $taskElasticHome)) {
    tar.exe -xf $taskArchive -C $taskElasticRoot
    if ($LASTEXITCODE -ne 0) { throw 'Elastic archive extraction failed' }
}
if (Get-NetTCPConnection -State Listen -LocalPort 19200,19300 -ErrorAction SilentlyContinue) {
    throw 'A task-local Elastic port is already occupied; inspect before starting another process'
}
$taskElasticConfig = @'
cluster.name: pause-p00-local
node.name: pause-p00-node
discovery.type: single-node
network.host: 127.0.0.1
http.port: 19200
transport.port: 19300
xpack.security.enabled: false
xpack.security.autoconfiguration.enabled: false
xpack.license.self_generated.type: basic
xpack.ml.enabled: false
xpack.watcher.enabled: false
ingest.geoip.downloader.enabled: false
'@
[IO.File]::WriteAllText((Join-Path $taskElasticHome 'config/elasticsearch.yml'), $taskElasticConfig)
[IO.File]::WriteAllText((Join-Path $taskElasticHome 'config/jvm.options.d/p00.options'), "-Xms256m`n-Xmx512m`n-XX:ActiveProcessorCount=2`n")
$taskLauncher = Start-Process -FilePath $env:ComSpec -ArgumentList '/d','/c',('"' + (Join-Path $taskElasticHome 'bin/elasticsearch.bat') + '"') -WindowStyle Hidden -WorkingDirectory $taskElasticHome -RedirectStandardOutput (Join-Path $taskElasticRoot 'stdout.log') -RedirectStandardError (Join-Path $taskElasticRoot 'stderr.log') -PassThru
[IO.File]::WriteAllText((Join-Path $taskElasticRoot 'launcher-pid.txt'), [string]$taskLauncher.Id)
Write-Output "Elastic launcher PID $($taskLauncher.Id); loopback ports 19200/19300; run status after startup."
