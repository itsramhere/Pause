param([string]$BaseURL='http://127.0.0.1:8082',[ValidateSet('Prepare','Void','Read')][string]$Action='Read',[string]$IntentID='',[string]$EvidencePath='data/p02-sandbox-smoke.json')
$ErrorActionPreference='Stop'
$taskUri=[Uri]$BaseURL
if($taskUri.Host -notin @('127.0.0.1','localhost') -or $taskUri.Scheme -ne 'http'){throw 'Local sandbox smoke only.'}
if(-not $env:SEED_PASSWORD){throw 'Set SEED_PASSWORD to the existing synthetic senior account password.'}
$taskOrigin=$BaseURL.TrimEnd('/');$taskHeaders=@{Origin=$taskOrigin}
$taskLogin=Invoke-RestMethod "$taskOrigin/api/auth/login" -Method Post -Headers $taskHeaders -ContentType 'application/json' -Body (@{email='senior1@example.test';password=$env:SEED_PASSWORD}|ConvertTo-Json -Compress) -SessionVariable taskSession
$taskHeaders['X-CSRF-Token']=$taskLogin.csrf_token
try{
 if($Action -eq 'Prepare'){
  if(Test-Path -LiteralPath $EvidencePath){throw 'Existing evidence found. Use Read/Void with its intent ID; do not create another order.'}
  $taskConfig=Invoke-RestMethod "$taskOrigin/api/configuration" -WebSession $taskSession
  if($taskConfig.payment_status -ne 'SANDBOX_AUTHORIZATION_ONLY'){throw 'Sandbox payment adapter unavailable.'}
  $taskContacts=Invoke-RestMethod "$taskOrigin/api/contacts" -WebSession $taskSession
  $taskHeaders['Idempotency-Key']=[Guid]::NewGuid().ToString()
  $taskIntake=@{amount='1.00';currency='USD';merchant_id=$taskConfig.merchant_id;subject_contact_id=$taskContacts[0].id;purpose='Synthetic P02 authorize and void smoke';message='Synthetic sandbox merchant purchase for lifecycle verification only.';history_consent=$false}
  $taskIntent=Invoke-RestMethod "$taskOrigin/api/intents" -Method Post -Headers $taskHeaders -ContentType 'application/json' -Body ($taskIntake|ConvertTo-Json -Compress) -WebSession $taskSession
  $IntentID=$taskIntent.id
  # Persist the identity before requesting a provider order, even if the response is lost.
  @{intent_id=$IntentID;amount_minor=100;currency='USD';merchant_id=$taskConfig.merchant_id;environment='sandbox';authorization_void_smoke='NOT_EXECUTED'}|ConvertTo-Json|Out-File -Encoding utf8 $EvidencePath
  $taskHeaders['Idempotency-Key']="order_$($IntentID)_1"
  Invoke-RestMethod "$taskOrigin/api/intents/$IntentID/paypal-order" -Method Post -Headers $taskHeaders -ContentType 'application/json' -Body '{"expected_version":1}' -WebSession $taskSession|Out-Null
  for($n=0;$n -lt 25;$n++){$taskIntent=Invoke-RestMethod "$taskOrigin/api/intents/$IntentID" -WebSession $taskSession;if($taskIntent.checkout.provider_order_id){break};Start-Sleep -Seconds 1}
 }else{if(-not $IntentID){throw 'IntentID is required.'};$taskIntent=Invoke-RestMethod "$taskOrigin/api/intents/$IntentID" -WebSession $taskSession}
 if($Action -eq 'Void'){
  if($taskIntent.payment_status -ne 'HELD'){throw 'The smoke must begin with a provider-confirmed uncaptured HELD authorization.'}
  Invoke-RestMethod "$taskOrigin/api/intents/$IntentID/cancel" -Method Post -Headers $taskHeaders -ContentType 'application/json' -Body (@{expected_version=$taskIntent.version}|ConvertTo-Json -Compress) -WebSession $taskSession|Out-Null
  for($n=0;$n -lt 25;$n++){$taskIntent=Invoke-RestMethod "$taskOrigin/api/intents/$IntentID" -WebSession $taskSession;if($taskIntent.payment_status -eq 'VOIDED'){break};Start-Sleep -Seconds 1}
 }
 $taskDiagnostics=Invoke-RestMethod "$taskOrigin/api/intents/$IntentID/payment-diagnostics" -WebSession $taskSession
 $taskResult=@{environment='sandbox';intent_id=$IntentID;version=$taskIntent.version;amount_minor=$taskIntent.binding.amount_minor;currency=$taskIntent.binding.currency;merchant_id=$taskIntent.binding.payee_id;payment_status=$taskIntent.payment_status;provider_order_id=$taskIntent.checkout.provider_order_id;authorization_id=$taskIntent.checkout.authorization_id;capture_id=$taskIntent.checkout.capture_id;decision_deadline=$taskIntent.decision_deadline;observed_at=[DateTime]::UtcNow.ToString('o');operations=$taskDiagnostics.operations;authorization_void_smoke=$(if($taskIntent.payment_status -eq 'VOIDED' -and $taskIntent.checkout.authorization_id){'PASS'}else{'NOT_COMPLETED'})}
 $taskResult|ConvertTo-Json -Depth 8|Out-File -Encoding utf8 $EvidencePath
 $taskResult|ConvertTo-Json -Depth 8
}finally{Invoke-RestMethod "$taskOrigin/api/auth/logout" -Method Post -Headers $taskHeaders -ContentType 'application/json' -Body '{}' -WebSession $taskSession|Out-Null}
