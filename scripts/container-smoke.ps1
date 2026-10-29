# Local isolated packaging drill. No provider credentials, real payments, public
# listener, DNS, synthetic-account passwords or production data are used.
param([switch]$Stop)
$ErrorActionPreference='Stop'
$taskRoot=(Resolve-Path (Join-Path $PSScriptRoot '..')).Path
$taskConfig=Join-Path $taskRoot 'data/p06-container.env'
$taskCompose=Join-Path $taskRoot 'data/p06-compose.env'
$taskOverride=Join-Path $taskRoot 'data/p06-compose.override.yaml'
if (-not $Stop -and -not (Test-Path -LiteralPath $taskConfig)) {
 $taskKey=[byte[]]::new(32);$taskRng=[Security.Cryptography.RandomNumberGenerator]::Create();$taskRng.GetBytes($taskKey);$taskRng.Dispose()
 $taskToken=[Convert]::ToBase64String($taskKey)+[guid]::NewGuid().ToString('N')
 $taskPassword=[guid]::NewGuid().ToString('N')
 [IO.File]::WriteAllText($taskConfig,@"
APP_ENV=test
APP_BASE_URL=http://127.0.0.1:18086
DATABASE_URL=postgres://pause:$taskPassword@postgres:5432/pause?sslmode=disable&pool_max_conns=5
MESSAGE_ENCRYPTION_KEY=$([Convert]::ToBase64String($taskKey))
PAYPAL_MERCHANT_ID=P06_CONTAINER_SYNTHETIC
PAYPAL_MERCHANT_LABEL=Synthetic Container Store
ML_SERVICE_TOKEN=$taskToken
TRUSTED_PROXY_CIDRS=172.30.42.0/24
"@)
 [IO.File]::WriteAllText($taskCompose,@"
RELEASE_TAG=p06-local
APP_ENV_FILE=$($taskConfig.Replace('\','/'))
ML_SERVICE_TOKEN=$taskToken
POSTGRES_PASSWORD=$taskPassword
PAUSE_HOSTNAME=unused.invalid
"@)
 [IO.File]::WriteAllText($taskOverride,"services:`n  api:`n    ports: [`"127.0.0.1:18086:8080`"]`n")
}
$taskArgs=@('compose','--env-file',$taskCompose,'-p','pause-p06-check','-f',(Join-Path $taskRoot 'deploy/compose.yaml'),'-f',$taskOverride)
function Invoke-Compose {
 # Windows PowerShell treats Docker's ordinary progress on stderr as errors.
 # Judge success by its exit code, then restore the caller's error preference.
 $taskPreviousPreference=$ErrorActionPreference
 $ErrorActionPreference='Continue'
 & docker @taskArgs @args 2>&1 | ForEach-Object { $_.ToString() }
 $taskExitCode=$LASTEXITCODE
 $ErrorActionPreference=$taskPreviousPreference
 if($taskExitCode -ne 0){throw 'Container step failed; inspect ignored log without publishing secrets.'}
}
if($Stop){Invoke-Compose down;exit}
Invoke-Compose config --quiet
Invoke-Compose up -d --wait postgres elastic inference
Invoke-Compose run --rm bootstrap /app/bin/migrate
Invoke-Compose run --rm bootstrap /app/bin/investigation-setup
Invoke-Compose run --rm bootstrap /app/bin/history-setup
Invoke-Compose up -d api worker
Write-Output 'Local package running on loopback 18086. release-readyz must stay unavailable without PayPal config; no real payment evidence is claimed.'
