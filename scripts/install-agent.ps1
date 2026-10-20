param(
  [Parameter(Mandatory=$true)][string]$InstallDir,
  [string]$TaskName = 'MAA Control Center Agent'
)
$ErrorActionPreference='Stop'
$exe=Join-Path $InstallDir 'maa-agent-windows-amd64.exe'
$config=Join-Path $InstallDir 'agent.private.json'
if (!(Test-Path -LiteralPath $exe) -or !(Test-Path -LiteralPath $config)) { throw 'Agent binary and private configuration are required.' }
if (Get-ScheduledTask -TaskName $TaskName -ErrorAction SilentlyContinue) { throw 'Task already exists. Inspect it before replacing it.' }
$action=New-ScheduledTaskAction -Execute $exe -Argument ('--config-file "'+$config+'"') -WorkingDirectory $InstallDir
$trigger=New-ScheduledTaskTrigger -AtLogOn -User ([System.Security.Principal.WindowsIdentity]::GetCurrent().Name)
$principal=New-ScheduledTaskPrincipal -UserId ([System.Security.Principal.WindowsIdentity]::GetCurrent().Name) -LogonType Interactive -RunLevel Limited
$settings=New-ScheduledTaskSettingsSet -StartWhenAvailable -RestartCount 3 -RestartInterval (New-TimeSpan -Minutes 1) -ExecutionTimeLimit ([TimeSpan]::Zero) -MultipleInstances IgnoreNew
Register-ScheduledTask -TaskName $TaskName -Action $action -Trigger $trigger -Principal $principal -Settings $settings -Description 'Read-only MAA discovery and HTTPS heartbeat. No production configuration writes.' | Out-Null
Start-ScheduledTask -TaskName $TaskName
Write-Output 'Dedicated read-only Agent task registered and started.'
