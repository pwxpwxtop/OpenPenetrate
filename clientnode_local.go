package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

const localTaskName = "IntranetPenetrationFrpc"
const localRunKeyPath = `HKCU\Software\Microsoft\Windows\CurrentVersion\Run`

func localNodeDir(service *ProxyService) string {
	return filepath.Join(service.dir, "local-frpc")
}

func localConfigFiles(dir string) []string {
	matches, err := filepath.Glob(filepath.Join(dir, "frpc*.toml"))
	if err != nil {
		return nil
	}
	return matches
}

func (s *ProxyService) testLocalNode() (string, error) {
	parts := []string{"连接成功", "本机", osLabel(runtime.GOOS), runtime.GOARCH}
	dir := localNodeDir(s)
	binPath := filepath.Join(dir, "frpc.exe")
	if info, err := os.Stat(binPath); err == nil && info.Size() > 0 {
		if out, err := runLocalCommand(binPath, "-v"); err == nil && strings.TrimSpace(out) != "" {
			parts = append(parts, "已安装 "+strings.TrimSpace(out))
		} else {
			parts = append(parts, "已安装 frpc")
		}
		if count := len(localConfigFiles(dir)); count > 0 {
			parts = append(parts, fmt.Sprintf("%d 个实例配置", count))
		}
	} else {
		parts = append(parts, "未安装 frpc")
	}
	return strings.Join(parts, " · "), nil
}

func (s *ProxyService) detectLocalNode(node ClientNode) ClientNode {
	node.OS = runtime.GOOS
	dir := localNodeDir(s)
	binPath := filepath.Join(dir, "frpc.exe")
	configs := localConfigFiles(dir)

	_, binErr := os.Stat(binPath)
	node.Installed = binErr == nil
	node.Configured = len(configs) > 0
	node.Status = ""

	if node.Installed && node.Configured {
		if localProcessRunning(binPath) {
			node.Status = "active"
		} else {
			node.Status = "inactive"
		}
		if out, err := runLocalCommand(binPath, "-v"); err == nil {
			node.Message = strings.TrimSpace(out)
		}
	} else if node.Installed {
		node.Status = "installed"
		node.Message = "frpc 已安装，等待关联本地代理"
	}

	s.persistNode(node)
	return node
}

func localTaskExists() bool {
	script := `if (Get-ScheduledTask | Where-Object { $_.TaskName -like 'IntranetPenetrationFrpc*' }) { 'yes' } else { 'no' }`
	out, err := runLocalCommand("powershell", "-NoProfile", "-ExecutionPolicy", "Bypass", "-Command", script)
	if err != nil {
		return false
	}
	return strings.Contains(strings.ToLower(out), "yes")
}

func localRunEntryRegistered() bool {
	out, err := runLocalCommand("reg", "query", localRunKeyPath)
	return err == nil && strings.Contains(out, localTaskName)
}

func localRegisterRunEntryNamed(name, binPath, cfgPath string) error {
	value := fmt.Sprintf(`"%s" -c "%s"`, binPath, cfgPath)
	out, err := runLocalCommand("reg", "add", localRunKeyPath, "/v", name, "/t", "REG_SZ", "/d", value, "/f")
	if err != nil {
		return fmt.Errorf("写入登录自启动失败: %s", strings.TrimSpace(out))
	}
	return nil
}

func localRemoveRunEntry(name string) {
	_, _ = runLocalCommand("reg", "delete", localRunKeyPath, "/v", name, "/f")
}

func localProcessRunning(binPath string) bool {
	script := fmt.Sprintf(`if (Get-Process -Name frpc -ErrorAction SilentlyContinue | Where-Object { $_.Path -eq '%s' }) { 'running' } else { 'stopped' }`, escapePowerShell(binPath))
	out, err := runLocalCommand("powershell", "-NoProfile", "-ExecutionPolicy", "Bypass", "-Command", script)
	if err != nil {
		return false
	}
	return strings.Contains(strings.ToLower(out), "running")
}

func localStartProcess(binPath, cfgPath string) error {
	cmd := exec.Command(binPath, "-c", cfgPath)
	hideCommandWindow(cmd)
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

func localStopProcess(binPath string) error {
	script := fmt.Sprintf(`Get-Process -Name frpc -ErrorAction SilentlyContinue | Where-Object { $_.Path -eq '%s' } | Stop-Process -Force`, escapePowerShell(binPath))
	out, err := runLocalCommand("powershell", "-NoProfile", "-ExecutionPolicy", "Bypass", "-Command", script)
	if err != nil {
		return fmt.Errorf("%s", strings.TrimSpace(out))
	}
	return nil
}

// localStartAll 先停止已有实例，再按配置文件启动每个 frpc 进程。
func localStartAll(binPath string, configs []string) error {
	if localProcessRunning(binPath) {
		_ = localStopProcess(binPath)
	}
	for _, cfgPath := range configs {
		if err := localStartProcess(binPath, cfgPath); err != nil {
			return err
		}
	}
	return nil
}

func (s *ProxyService) deployLocalNode(node ClientNode, groups []nodeDeployGroup, emit func(string)) (ClientNode, error) {
	if runtime.GOOS != "windows" {
		return node, errors.New("本机后台挂载目前仅支持 Windows；macOS/Linux 请使用 SSH 挂载")
	}
	if runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64" {
		return node, fmt.Errorf("不支持的本机架构: %s", runtime.GOARCH)
	}

	binary, err := s.downloadFrpcBinary(frpPlatformInfo{os: "windows", arch: runtime.GOARCH}, emit)
	if err != nil {
		emit("下载 frpc 失败: " + err.Error())
		return node, err
	}

	dir := localNodeDir(s)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		emit("创建本机目录失败: " + err.Error())
		return node, err
	}
	binPath := filepath.Join(dir, "frpc.exe")
	if err := os.WriteFile(binPath, binary, 0o755); err != nil {
		emit("写入 frpc.exe 失败: " + err.Error())
		return node, err
	}
	emit("frpc.exe 已写入本机")

	node.OS = "windows"
	if len(groups) == 0 {
		node.Installed = true
		node.Configured = false
		node.Status = "installed"
		node.Message = "frpc 已安装，等待关联本地代理"
		s.persistNode(node)
		emit("安装完成。请先关联本地代理，再重新部署以创建后台任务")
		return node, nil
	}

	configs := make([]string, 0, len(groups))
	taskNames := make([]string, 0, len(groups))
	for _, group := range groups {
		suffix := groupUnitSuffix(group.Server.ID)
		cfgPath := filepath.Join(dir, "frpc-"+suffix+".toml")
		logPath := filepath.ToSlash(filepath.Join(dir, "frpc-"+suffix+".log"))
		config := buildNodeFrpcConfig(group.Server, group.Proxies, "windows")
		config = strings.Replace(config, "C:/ProgramData/frp/frpc.log", logPath, 1)
		if err := os.WriteFile(cfgPath, []byte(config), 0o600); err != nil {
			emit("写入配置失败: " + err.Error())
			return node, err
		}
		configs = append(configs, cfgPath)
		taskNames = append(taskNames, localTaskName+"-"+suffix)
	}
	// 清理旧版单实例配置，避免重复启动。
	_ = os.Remove(filepath.Join(dir, "frpc.toml"))
	emit(fmt.Sprintf("配置已写入本机（%d 个实例）", len(groups)))

	quoted := func(values []string) string {
		parts := make([]string, len(values))
		for i, value := range values {
			parts[i] = "'" + escapePowerShell(value) + "'"
		}
		return strings.Join(parts, ",")
	}

	script := fmt.Sprintf(`$ErrorActionPreference = 'Stop'
$bin = '%s'
$configs = @(%s)
$names = @(%s)
if (Get-ScheduledTask -TaskName '%s' -ErrorAction SilentlyContinue) {
  Stop-ScheduledTask -TaskName '%s' -ErrorAction SilentlyContinue
  Unregister-ScheduledTask -TaskName '%s' -Confirm:$false
}
$trigger = New-ScheduledTaskTrigger -AtLogOn
$settings = New-ScheduledTaskSettingsSet -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries -RestartCount 3 -RestartInterval (New-TimeSpan -Minutes 1)
for ($i = 0; $i -lt $configs.Count; $i++) {
  $taskName = $names[$i]
  if (Get-ScheduledTask -TaskName $taskName -ErrorAction SilentlyContinue) {
    Stop-ScheduledTask -TaskName $taskName -ErrorAction SilentlyContinue
    Unregister-ScheduledTask -TaskName $taskName -Confirm:$false
  }
  $action = New-ScheduledTaskAction -Execute $bin -Argument ('-c "{0}"' -f $configs[$i])
  Register-ScheduledTask -TaskName $taskName -Action $action -Trigger $trigger -Settings $settings -Force | Out-Null
  Start-ScheduledTask -TaskName $taskName
}
Start-Sleep -Seconds 2
"state=" + (Get-ScheduledTask -TaskName $names[0]).State
`, escapePowerShell(binPath), quoted(configs), quoted(taskNames), localTaskName, localTaskName, localTaskName)

	out, err := runLocalCommand("powershell", "-NoProfile", "-ExecutionPolicy", "Bypass", "-Command", script)
	if err != nil {
		emit("创建计划任务失败（通常需要管理员权限），改用当前用户登录自启动")
		for i, cfgPath := range configs {
			if regErr := localRegisterRunEntryNamed(taskNames[i], binPath, cfgPath); regErr != nil {
				emit("本机后台挂载失败: " + regErr.Error())
				return node, regErr
			}
		}
		localRemoveRunEntry(localTaskName)
		if startErr := localStartAll(binPath, configs); startErr != nil {
			emit("启动 frpc 失败: " + startErr.Error())
			return node, startErr
		}
		node.Installed = true
		node.Configured = true
		node.Status = "active"
		node.Message = fmt.Sprintf("本机 frpc 已部署（%d 个实例，登录自启动）", len(groups))
		s.persistNode(node)
		emit(fmt.Sprintf("部署完成，已启动 %d 个 frpc 实例", len(groups)))
		return node, nil
	}

	// 计划任务模式成功，移除旧版单实例注册表值，避免重复启动。
	localRemoveRunEntry(localTaskName)
	values := parseKeyValues(out)
	node.Installed = true
	node.Configured = true
	if values["state"] == "Running" {
		node.Status = "active"
	} else {
		node.Status = "inactive"
	}
	node.Message = fmt.Sprintf("本机 frpc 已部署（%d 个实例）", len(groups))
	s.persistNode(node)
	emit(fmt.Sprintf("部署完成，创建了 %d 个计划任务，状态: %s", len(groups), values["state"]))
	return node, nil
}

func escapePowerShell(value string) string {
	return strings.ReplaceAll(value, "'", "''")
}
