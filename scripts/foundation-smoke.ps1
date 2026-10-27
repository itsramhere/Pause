param([string]$BaseURL='http://127.0.0.1:5173', [string]$Email='senior1@example.test')
$ErrorActionPreference='Stop'
$taskUri=[Uri]$BaseURL
if ($taskUri.Host -notin @('127.0.0.1','localhost') -or $taskUri.Scheme -ne 'http') { throw 'This synthetic smoke check is restricted to local development.' }
if (-not $env:SEED_PASSWORD) { throw 'Set SEED_PASSWORD to the password used by the explicit development seed command.' }
$taskOrigin=$BaseURL.TrimEnd('/')
$taskHeaders=@{Origin=$taskOrigin}
$taskLoginBody=@{email=$Email;password=$env:SEED_PASSWORD} | ConvertTo-Json -Compress
$taskLogin=Invoke-RestMethod "$taskOrigin/api/auth/login" -Method Post -Headers $taskHeaders -ContentType 'application/json' -Body $taskLoginBody -SessionVariable taskSession
$taskHeaders['X-CSRF-Token']=$taskLogin.csrf_token
try {
 $taskReady=Invoke-RestMethod "$taskOrigin/readyz"
 if ($taskReady.status -ne 'ready') { throw 'Application is not ready.' }
 $taskConfig=Invoke-RestMethod "$taskOrigin/api/configuration" -WebSession $taskSession
 $taskContacts=Invoke-RestMethod "$taskOrigin/api/contacts" -WebSession $taskSession
 if (-not $taskContacts -or $taskContacts[0].name -notlike 'Synthetic *') { throw 'Expected a synthetic enrolled contact.' }
 $taskInput=@{amount='40.00';currency='USD';merchant_id=$taskConfig.merchant_id;subject_contact_id=$taskContacts[0].id;purpose='Synthetic foundation startup check';message='Synthetic fixture only: transport purchase requested for this merchant.';history_consent=$true}
 $taskHeaders['Idempotency-Key']=[Guid]::NewGuid().ToString()
 $taskBody=$taskInput | ConvertTo-Json -Compress
 $taskIntent=Invoke-RestMethod "$taskOrigin/api/intents" -Method Post -Headers $taskHeaders -ContentType 'application/json' -Body $taskBody -WebSession $taskSession
 $taskReplay=Invoke-RestMethod "$taskOrigin/api/intents" -Method Post -Headers $taskHeaders -ContentType 'application/json' -Body $taskBody -WebSession $taskSession
 if ($taskIntent.id -ne $taskReplay.id -or $taskIntent.binding.amount_minor -ne 4000 -or $taskIntent.checkout.status -ne 'UNAVAILABLE') { throw 'Intake/idempotency contract failed.' }
 $taskHeaders.Remove('Idempotency-Key')
 $taskInput.amount='45.01';$taskInput['expected_version']=1;$taskInput['expected_consent_version']=$taskIntent.consent.version
 $taskCorrected=Invoke-RestMethod "$taskOrigin/api/intents/$($taskIntent.id)/corrections" -Method Post -Headers $taskHeaders -ContentType 'application/json' -Body ($taskInput | ConvertTo-Json -Compress) -WebSession $taskSession
 if ($taskCorrected.version -ne 2 -or $taskCorrected.binding.amount_minor -ne 4501 -or $taskCorrected.content_digest -eq $taskIntent.content_digest) { throw 'Correction binding failed.' }
 $taskConsentBody=@{expected_version=$taskCorrected.consent.version;history=$false} | ConvertTo-Json -Compress
 $taskWithdrawn=Invoke-RestMethod "$taskOrigin/api/intents/$($taskIntent.id)/consent" -Method Put -Headers $taskHeaders -ContentType 'application/json' -Body $taskConsentBody -WebSession $taskSession
 $taskRead=Invoke-RestMethod "$taskOrigin/api/intents/$($taskIntent.id)" -WebSession $taskSession
 if ($taskRead.consent.history -or $taskRead.consent.version -ne 2) { throw 'Consent persistence failed.' }
 [PSCustomObject]@{Result='PASS';MigrationVersion=$taskReady.migration_version;Version=$taskRead.version;AmountMinor=$taskRead.binding.amount_minor;ConsentVersion=$taskWithdrawn.consent.version;HistoryAllowed=$taskRead.consent.history;PaymentStatus=$taskRead.payment_status;Checkout=$taskRead.checkout.status} | Format-List
} finally {
 Invoke-RestMethod "$taskOrigin/api/auth/logout" -Method Post -Headers $taskHeaders -ContentType 'application/json' -Body '{}' -WebSession $taskSession | Out-Null
}
