param([string]$TaskName = 'MAA Control Center Agent')
$ErrorActionPreference='Stop'
# Only the task created by install-agent.ps1; preserve binary, snapshots and all MAA data.
$task=Get-ScheduledTask -TaskName $TaskName -ErrorAction SilentlyContinue
if ($task) { Stop-ScheduledTask -TaskName $TaskName; Unregister-ScheduledTask -TaskName $TaskName -Confirm:$false }
Write-Output 'Control Center Agent task removed. MAA and application data preserved.'
