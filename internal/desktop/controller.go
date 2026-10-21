package desktop

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"

	"github.com/yumi233/maa-control-center/internal/executil"
)

type Snapshot struct {
	Running bool `json:"running"`
	Known   bool `json:"known"`
	Idle    bool `json:"idle"`
	PID     int  `json:"pid"`
}
type Controller struct{}

// Automation invokes the same native UI commands as the user; no injected DLL or shell input.
const script = `$ErrorActionPreference='Stop'
Add-Type -AssemblyName UIAutomationClient
$p=@(Get-Process -Name MAA -ErrorAction SilentlyContinue | Where-Object {$_.Path -eq $env:MAACC_CONTROL_EXE})
if($p.Count -eq 0){if($env:MAACC_CONTROL_ACTION -eq 'probe'){@{running=$false;known=$true;idle=$true;pid=0}|ConvertTo-Json -Compress;exit};throw 'MAA not running'}
if($p.Count -ne 1){throw 'Ambiguous MAA process'}
$p=$p[0]
if($p.SessionId -ne (Get-Process -Id $PID).SessionId){throw 'MAA requires the same interactive session'}
if($p.MainWindowHandle -eq 0){
 # MAA 6.20 secondary launch signals the existing instance to show its own window.
 $wake=Start-Process -FilePath $env:MAACC_CONTROL_EXE -WorkingDirectory (Split-Path $env:MAACC_CONTROL_EXE) -ArgumentList '--skip-startup-auto-run' -PassThru
 for($i=0;$i -lt 32;$i++){Start-Sleep -Milliseconds 250;$p.Refresh();if($p.MainWindowHandle -ne 0){break}}
 if($p.MainWindowHandle -eq 0){throw 'No interactive MAA window'}
}
$r=[System.Windows.Automation.AutomationElement]::FromHandle($p.MainWindowHandle)
$bc=New-Object System.Windows.Automation.PropertyCondition([System.Windows.Automation.AutomationElement]::ControlTypeProperty,[System.Windows.Automation.ControlType]::Button)
$sc=New-Object System.Windows.Automation.PropertyCondition([System.Windows.Automation.AutomationElement]::NameProperty,'Link Start!')
$start=$r.FindFirst([System.Windows.Automation.TreeScope]::Descendants,(New-Object System.Windows.Automation.AndCondition($bc,$sc)))
$idle=$null -ne $start -and $start.Current.IsEnabled -and !$start.Current.IsOffscreen
if($env:MAACC_CONTROL_ACTION -eq 'probe'){@{running=$true;known=$true;idle=$idle;pid=$p.Id}|ConvertTo-Json -Compress;exit}
if($env:MAACC_CONTROL_ACTION -eq 'close'){
 if(!$idle){throw 'MAA is not verified idle'}
 if(!$p.CloseMainWindow()){throw 'MAA did not accept normal close'}
 if(!$p.WaitForExit(12000)){throw 'MAA close not confirmed'}
 'ok';exit
}
if($env:MAACC_CONTROL_ACTION -eq 'start'){
 if(!$idle){throw 'MAA is not verified idle'}
 ([System.Windows.Automation.InvokePattern]$start.GetCurrentPattern([System.Windows.Automation.InvokePattern]::Pattern)).Invoke();'ok';exit
}
if($env:MAACC_CONTROL_ACTION -eq 'stop'){
 if($idle){'ok';exit}
 $stop=$null
 foreach($name in @('停止','Stop','停止任务','等待战斗结束后停止')){
  $nc=New-Object System.Windows.Automation.PropertyCondition([System.Windows.Automation.AutomationElement]::NameProperty,$name)
  $item=$r.FindFirst([System.Windows.Automation.TreeScope]::Descendants,(New-Object System.Windows.Automation.AndCondition($bc,$nc)))
  if($null -ne $item -and $item.Current.IsEnabled -and !$item.Current.IsOffscreen){$stop=$item;break}
 }
 if($null -eq $stop){throw 'Stop control unavailable'}
 ([System.Windows.Automation.InvokePattern]$stop.GetCurrentPattern([System.Windows.Automation.InvokePattern]::Pattern)).Invoke();'ok';exit
}
throw 'Unknown action'
`

func control(root, action string) ([]byte, error) {
	if runtime.GOOS != "windows" {
		return nil, fmt.Errorf("仅 Windows 交互用户会话支持实际任务控制")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script)
	executil.HideConsole(cmd)
	cmd.Env = append(os.Environ(), "MAACC_CONTROL_EXE="+filepath.Join(root, "MAA.exe"), "MAACC_CONTROL_ACTION="+action)
	b, e := cmd.Output()
	if e != nil {
		return nil, fmt.Errorf("MAA 窗口状态或权限未确认，请保持当前用户登录并检查窗口；未强制结束进程")
	}
	return b, nil
}
func (Controller) Probe(root string) (Snapshot, error) {
	b, e := control(root, "probe")
	var s Snapshot
	if e == nil {
		e = json.Unmarshal(b, &s)
	}
	return s, e
}
func (Controller) CloseIdle(root string) error { _, e := control(root, "close"); return e }
func (Controller) Start(root string) error     { _, e := control(root, "start"); return e }
func (Controller) Stop(root string) error      { _, e := control(root, "stop"); return e }
func (Controller) Launch(root string) error {
	if runtime.GOOS != "windows" {
		return fmt.Errorf("需要 Windows")
	}
	// Suppress the user's startup auto-run; only the explicit job invokes Start.
	cmd := exec.Command(filepath.Join(root, "MAA.exe"), "--skip-startup-auto-run")
	cmd.Dir = root
	if e := cmd.Start(); e != nil {
		return fmt.Errorf("MAA 启动失败")
	}
	return cmd.Process.Release()
}
func (c Controller) Prepare(root string) (func(), error) {
	s, e := c.Probe(root)
	if e != nil {
		return nil, e
	}
	if !s.Running {
		return nil, nil
	}
	if !s.Known || !s.Idle {
		return nil, fmt.Errorf("MAA 正在运行任务，配置已排队等待空闲")
	}
	if e = c.CloseIdle(root); e != nil {
		return nil, e
	}
	return func() { _ = c.Launch(root) }, nil
}
