package main

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/wailsapp/wails/v3/pkg/application"
	"golang.org/x/crypto/ssh"
)

const nodeMaxLogLines = 500

// NodeDeployLogEvent 是客户端节点部署过程的日志事件。
type NodeDeployLogEvent struct {
	NodeID string `json:"nodeId"`
	Line   string `json:"line"`
}

func nodeSSHTarget(node ClientNode) ProxyServer {
	return ProxyServer{
		Host:       node.Host,
		SSHPort:    node.SSHPort,
		Username:   node.Username,
		AuthType:   node.AuthType,
		Password:   node.Password,
		PrivateKey: node.PrivateKey,
	}
}

// TestNode 测试客户端节点的 SSH 连接，并返回系统信息。
func (s *ProxyService) TestNode(node ClientNode) (string, error) {
	if node.AuthType == "local" {
		return s.testLocalNode()
	}

	client, err := dialSSH(nodeSSHTarget(node))
	if err != nil {
		return "", err
	}
	defer client.Close()

	osName, err := detectRemoteOS(client)
	if err != nil {
		return "", err
	}
	arch, err := detectRemoteArch(client, osName)
	if err != nil {
		return "", err
	}

	parts := []string{"连接成功", osLabel(osName), arch}
	if osName == "windows" {
		version, _ := runPowerShell(client, "if (Test-Path 'C:\\ProgramData\\frp\\frpc.exe') { & 'C:\\ProgramData\\frp\\frpc.exe' -v }")
		if strings.TrimSpace(version) != "" {
			parts = append(parts, "已安装 "+strings.TrimSpace(version))
		} else {
			parts = append(parts, "未安装 frpc")
		}
	} else {
		versionCmd := "if [ -x /opt/frp/frpc ]; then /opt/frp/frpc -v 2>/dev/null; elif [ -x /usr/local/bin/frpc ]; then /usr/local/bin/frpc -v 2>/dev/null; elif command -v frpc >/dev/null 2>&1; then frpc -v 2>/dev/null; fi"
		version, _ := runRemote(client, versionCmd)
		if strings.TrimSpace(version) != "" {
			parts = append(parts, "已安装 "+strings.TrimSpace(version))
		} else {
			parts = append(parts, "未安装 frpc")
		}
	}
	return strings.Join(parts, " · "), nil
}

// DetectNodeFrpc 检测节点上 frpc 的安装与运行状态。
func (s *ProxyService) DetectNodeFrpc(node ClientNode) (ClientNode, error) {
	if node.AuthType == "local" {
		return s.detectLocalNode(node), nil
	}

	client, err := dialSSH(nodeSSHTarget(node))
	if err != nil {
		return node, err
	}
	defer client.Close()

	osName, err := detectRemoteOS(client)
	if err != nil {
		return node, err
	}
	node.OS = osName

	switch osName {
	case "windows":
		script := `
$bin = 'C:\ProgramData\frp\frpc.exe'
"installed=" + (Test-Path $bin)
$configs = Get-ChildItem 'C:\ProgramData\frp\frpc*.toml' -ErrorAction SilentlyContinue
"configured=" + (($configs | Measure-Object).Count -gt 0)
$tasks = Get-ScheduledTask -ErrorAction SilentlyContinue | Where-Object { $_.TaskName -like 'IntranetPenetrationFrpc*' -or $_.TaskName -eq 'frpc' }
"running=" + ((($tasks | Where-Object { $_.State -eq 'Running' }) | Measure-Object).Count -gt 0)
`
		out, _ := runPowerShell(client, script)
		values := parseKeyValues(out)
		node.Installed = values["installed"] == "True"
		node.Configured = values["configured"] == "True"
		if values["running"] == "True" {
			node.Status = "active"
		} else if node.Configured {
			node.Status = "inactive"
		}
		if node.Installed {
			version, _ := runPowerShell(client, "& 'C:\\ProgramData\\frp\\frpc.exe' -v")
			node.Message = strings.TrimSpace(version)
		}
	case "darwin":
		version, _ := runRemote(client, "if [ -x /opt/frp/frpc ]; then /opt/frp/frpc -v 2>/dev/null; elif [ -x /usr/local/bin/frpc ]; then /usr/local/bin/frpc -v 2>/dev/null; fi")
		configured, _ := runRemote(client, "ls /etc/frp/frpc*.toml >/dev/null 2>&1 && echo yes || echo no")
		active, _ := runRemote(client, "launchctl list 2>/dev/null | grep -q 'com.frp.frpc' && echo active || echo inactive")
		node.Installed = strings.TrimSpace(version) != ""
		node.Configured = strings.TrimSpace(configured) == "yes"
		node.Status = strings.TrimSpace(active)
		node.Message = strings.TrimSpace(version)
	default:
		version, _ := runRemote(client, "if [ -x /opt/frp/frpc ]; then /opt/frp/frpc -v 2>/dev/null; elif command -v frpc >/dev/null 2>&1; then frpc -v 2>/dev/null; fi")
		configured, _ := runRemote(client, "ls /etc/frp/frpc*.toml >/dev/null 2>&1 && echo yes || echo no")
		activeCount, _ := runRemote(client, "systemctl list-units --type=service --state=active --no-legend 'frpc*' 2>/dev/null | wc -l")
		node.Installed = strings.TrimSpace(version) != ""
		node.Configured = strings.TrimSpace(configured) == "yes"
		if strings.TrimSpace(activeCount) != "" && strings.TrimSpace(activeCount) != "0" {
			node.Status = "active"
		} else if node.Configured {
			node.Status = "inactive"
		}
		node.Message = strings.TrimSpace(version)
	}

	if node.Status == "" && node.Installed {
		node.Status = "installed"
	}
	s.persistNode(node)
	return node, nil
}

// DeployNodeFrpc 在 Linux 节点上安装/更新 frpc，并注册 systemd 服务。
func (s *ProxyService) DeployNodeFrpc(nodeID string) (ClientNode, error) {
	s.mu.Lock()
	if err := s.ensureLoaded(); err != nil {
		s.mu.Unlock()
		return ClientNode{ID: nodeID}, err
	}

	var node ClientNode
	found := false
	for _, item := range s.cfg.Nodes {
		if item.ID == nodeID {
			node = item
			found = true
			break
		}
	}
	var proxies []LocalProxy
	var servers []ProxyServer
	if found {
		for _, item := range s.cfg.LocalProxies {
			if item.NodeID == nodeID {
				proxies = append(proxies, item)
			}
		}
		servers = append(servers, s.cfg.Servers...)
	}
	s.mu.Unlock()

	if !found {
		return node, errors.New("客户端节点不存在")
	}

	// 同一节点可以关联多台代理服务器，每台服务器会部署一个独立的 frpc 实例。
	groups, groupErr := groupNodeProxies(proxies, servers)
	if groupErr != nil {
		return node, groupErr
	}

	emit := func(line string) {
		s.appendNodeLog(nodeID, line)
	}

	s.nodeLogsMu.Lock()
	s.nodeLogs[nodeID] = nil
	s.nodeLogsMu.Unlock()

	if len(groups) > 0 {
		emit(fmt.Sprintf("本次配置 %d 个代理，涉及 %d 台代理服务器", len(proxies), len(groups)))
		for _, group := range groups {
			ports := make([]string, 0, len(group.Proxies))
			for _, proxy := range group.Proxies {
				ports = append(ports, strconv.Itoa(proxy.RemotePort))
			}
			emit(fmt.Sprintf("  %s: %s", group.Server.Name, strings.Join(ports, ", ")))
		}
	}

	if node.AuthType == "local" {
		return s.deployLocalNode(node, groups, emit)
	}

	emit("正在连接 " + net.JoinHostPort(node.Host, strconv.Itoa(node.SSHPort)) + " ...")
	client, err := dialSSH(nodeSSHTarget(node))
	if err != nil {
		emit("连接失败: " + err.Error())
		return node, err
	}
	defer client.Close()
	emit("SSH 连接成功")

	osName, err := detectRemoteOS(client)
	if err != nil {
		emit("识别系统失败: " + err.Error())
		return node, err
	}
	emit("识别系统: " + osLabel(osName))
	arch, err := detectRemoteArch(client, osName)
	if err != nil {
		emit("识别架构失败: " + err.Error())
		return node, err
	}
	platform, ok := frpPlatformFor(osName, arch)
	if !ok {
		err := fmt.Errorf("不支持的系统架构: %s/%s", osName, arch)
		emit(err.Error())
		return node, err
	}
	node.OS = osName
	emit(fmt.Sprintf("节点系统: %s / %s", osLabel(osName), arch))

	binary, err := s.downloadFrpcBinary(platform, emit)
	if err != nil {
		emit("下载 frpc 失败: " + err.Error())
		return node, err
	}

	if osName == "windows" {
		return s.deployWindowsNode(client, node, groups, binary, emit)
	}
	return s.deployUnixNode(client, node, groups, binary, emit)
}

// nodeDeployGroup 表示同一个节点上针对一台代理服务器的一组本地代理。
type nodeDeployGroup struct {
	Server  ProxyServer
	Proxies []LocalProxy
}

func groupNodeProxies(proxies []LocalProxy, servers []ProxyServer) ([]nodeDeployGroup, error) {
	serverMap := make(map[string]ProxyServer, len(servers))
	for _, server := range servers {
		serverMap[server.ID] = server
	}

	order := make([]string, 0)
	grouped := make(map[string][]LocalProxy)
	for _, proxy := range proxies {
		if proxy.ServerID == "" {
			return nil, fmt.Errorf("代理「%s」未关联代理服务器", proxy.Name)
		}
		if proxy.RemotePort < 1 || proxy.RemotePort > 65535 {
			return nil, fmt.Errorf("代理「%s」的远程访问端口无效", proxy.Name)
		}
		if _, ok := grouped[proxy.ServerID]; !ok {
			order = append(order, proxy.ServerID)
		}
		grouped[proxy.ServerID] = append(grouped[proxy.ServerID], proxy)
	}

	groups := make([]nodeDeployGroup, 0, len(order))
	for _, serverID := range order {
		server, ok := serverMap[serverID]
		if !ok {
			return nil, errors.New("本地代理关联的代理服务器不存在，请重新选择")
		}
		groups = append(groups, nodeDeployGroup{Server: server, Proxies: grouped[serverID]})
	}
	return groups, nil
}

func groupUnitSuffix(serverID string) string {
	suffix := strings.Map(func(r rune) rune {
		if (r >= '0' && r <= '9') || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
			return r
		}
		return -1
	}, serverID)
	if len(suffix) > 12 {
		suffix = suffix[:12]
	}
	return suffix
}

func (s *ProxyService) deployUnixNode(client *ssh.Client, node ClientNode, groups []nodeDeployGroup, binary []byte, emit func(string)) (ClientNode, error) {
	remoteTmp := "/tmp/frpc_new_" + node.ID
	if err := uploadBytes(client, binary, remoteTmp); err != nil {
		emit("上传 frpc 失败: " + err.Error())
		return node, err
	}
	emit("frpc 已上传")

	sudoPrefix, sudoStdin, err := sudoContext(client, node.AuthType, node.Password)
	if err != nil {
		emit(err.Error())
		return node, err
	}

	if len(groups) == 0 {
		script := fmt.Sprintf(`#!/usr/bin/env bash
set -euo pipefail
echo "==> 安装 frpc"
mkdir -p /opt/frp
install -m 0755 '%s' /opt/frp/frpc
rm -f '%s'
echo "frpc 已安装，尚未关联本地代理，服务未启动"
`, remoteTmp, remoteTmp)
		return s.runUnixInstallScript(client, node, script, sudoPrefix, sudoStdin, emit, false)
	}

	type unixUnit struct {
		name    string
		cfgPath string
		cfgB64  string
	}
	units := make([]unixUnit, 0, len(groups))
	for _, group := range groups {
		suffix := groupUnitSuffix(group.Server.ID)
		units = append(units, unixUnit{
			name:    "frpc-" + suffix,
			cfgPath: "/etc/frp/frpc-" + suffix + ".toml",
			cfgB64:  base64.StdEncoding.EncodeToString([]byte(buildNodeFrpcConfig(group.Server, group.Proxies, node.OS))),
		})
	}

	if node.OS == "darwin" {
		var builder strings.Builder
		builder.WriteString("#!/usr/bin/env bash\nset -euo pipefail\necho \"==> 安装 frpc\"\n")
		fmt.Fprintf(&builder, "mkdir -p /opt/frp /etc/frp /Library/LaunchDaemons\ninstall -m 0755 '%s' /opt/frp/frpc\n", remoteTmp)
		// 清理旧的单实例服务。
		builder.WriteString("launchctl bootout system /Library/LaunchDaemons/com.frp.frpc.plist 2>/dev/null || true\nrm -f /Library/LaunchDaemons/com.frp.frpc.plist\n")
		for _, unit := range units {
			label := "com.frp." + unit.name
			fmt.Fprintf(&builder, "printf '%%s' '%s' | base64 -d > '%s'\nchmod 600 '%s'\n", unit.cfgB64, unit.cfgPath, unit.cfgPath)
			fmt.Fprintf(&builder, "cat > '/Library/LaunchDaemons/%s.plist' <<'PLIST_EOF'\n", label)
			fmt.Fprintf(&builder, `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>%s</string>
  <key>ProgramArguments</key>
  <array>
    <string>/opt/frp/frpc</string>
    <string>-c</string>
    <string>%s</string>
  </array>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
  <key>StandardOutPath</key><string>/var/log/%s.log</string>
  <key>StandardErrorPath</key><string>/var/log/%s.log</string>
</dict>
</plist>
`, label, unit.cfgPath, unit.name, unit.name)
			builder.WriteString("PLIST_EOF\n")
			fmt.Fprintf(&builder, "chmod 644 '/Library/LaunchDaemons/%s.plist'\n", label)
			fmt.Fprintf(&builder, "launchctl bootout system '/Library/LaunchDaemons/%s.plist' 2>/dev/null || true\n", label)
			fmt.Fprintf(&builder, "launchctl bootstrap system '/Library/LaunchDaemons/%s.plist'\n", label)
			fmt.Fprintf(&builder, "launchctl enable system/%s\n", label)
			fmt.Fprintf(&builder, "launchctl kickstart -k system/%s\n", label)
		}
		builder.WriteString("sleep 2\nfailed=0\n")
		for _, unit := range units {
			label := "com.frp." + unit.name
			fmt.Fprintf(&builder, "if ! launchctl print system/%s >/dev/null 2>&1; then echo '%s 启动失败'; tail -n 30 /var/log/%s.log 2>/dev/null || true; failed=1; fi\n", label, unit.name, unit.name)
		}
		builder.WriteString("if [ \"$failed\" -ne 0 ]; then exit 1; fi\n")
		fmt.Fprintf(&builder, "rm -f '%s'\n", remoteTmp)
		fmt.Fprintf(&builder, "echo \"frpc 已启动（%d 个实例）\"\n", len(units))
		return s.runUnixInstallScript(client, node, builder.String(), sudoPrefix, sudoStdin, emit, true)
	}

	var builder strings.Builder
	builder.WriteString("#!/usr/bin/env bash\nset -euo pipefail\necho \"==> 安装 frpc\"\n")
	fmt.Fprintf(&builder, "mkdir -p /opt/frp /etc/frp\ninstall -m 0755 '%s' /opt/frp/frpc\n", remoteTmp)
	// 清理旧的单实例服务，避免与新实例重复注册端口。
	builder.WriteString("systemctl stop frpc 2>/dev/null || true\nsystemctl disable frpc 2>/dev/null || true\nrm -f /etc/systemd/system/frpc.service\n")
	for _, unit := range units {
		fmt.Fprintf(&builder, "printf '%%s' '%s' | base64 -d > '%s'\nchmod 600 '%s'\n", unit.cfgB64, unit.cfgPath, unit.cfgPath)
		fmt.Fprintf(&builder, `cat > '/etc/systemd/system/%s.service' <<'UNIT_EOF'
[Unit]
Description=frp client (%s)
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
Restart=always
RestartSec=5s
User=root
ExecStart=/opt/frp/frpc -c %s

[Install]
WantedBy=multi-user.target
UNIT_EOF
`, unit.name, unit.name, unit.cfgPath)
	}
	builder.WriteString("systemctl daemon-reload\n")
	for _, unit := range units {
		fmt.Fprintf(&builder, "systemctl enable %s >/dev/null 2>&1 || true\nsystemctl restart %s\n", unit.name, unit.name)
	}
	builder.WriteString("sleep 2\nfailed=0\n")
	for _, unit := range units {
		fmt.Fprintf(&builder, "if ! systemctl is-active --quiet %s; then echo '%s 启动失败'; journalctl -u %s -n 20 --no-pager || true; failed=1; fi\n", unit.name, unit.name, unit.name)
	}
	builder.WriteString("if [ \"$failed\" -ne 0 ]; then exit 1; fi\n")
	fmt.Fprintf(&builder, "rm -f '%s'\n", remoteTmp)
	fmt.Fprintf(&builder, "echo \"frpc 已启动（%d 个实例）\"\n", len(units))
	return s.runUnixInstallScript(client, node, builder.String(), sudoPrefix, sudoStdin, emit, true)
}

func (s *ProxyService) runUnixInstallScript(client *ssh.Client, node ClientNode, script, sudoPrefix, sudoStdin string, emit func(string), configured bool) (ClientNode, error) {
	scriptPath := "/tmp/node_frpc_install_" + node.ID + ".sh"
	if err := uploadBytes(client, []byte(script), scriptPath); err != nil {
		emit("上传安装脚本失败: " + err.Error())
		return node, err
	}
	if err := runStreaming(client, sudoPrefix+"bash "+scriptPath, sudoStdin, emit); err != nil {
		emit("安装失败: " + err.Error())
		return node, fmt.Errorf("部署 frpc 失败: %w", err)
	}
	_, _ = runRemote(client, "rm -f '"+scriptPath+"'")

	node.Installed = true
	node.Configured = configured
	if !configured {
		node.Status = "installed"
		node.Message = "frpc 已安装，等待关联本地代理"
	} else {
		// 安装脚本会在任一 frpc 实例启动失败时退出，因此到这里说明全部实例已运行。
		node.Status = "active"
		node.Message = "frpc 已部署"
	}
	s.persistNode(node)
	emit("部署完成，frpc 状态: " + node.Status)
	return node, nil
}

func (s *ProxyService) deployWindowsNode(client *ssh.Client, node ClientNode, groups []nodeDeployGroup, binary []byte, emit func(string)) (ClientNode, error) {
	emit("准备远程目录 ...")
	if out, err := runPowerShell(client, `New-Item -ItemType Directory -Force -Path 'C:\ProgramData\frp' | Out-Null; 'ok'`); err != nil {
		emit("创建目录失败: " + strings.TrimSpace(out))
		return node, fmt.Errorf("创建 Windows 远程目录失败: %w", err)
	}

	emit("上传 frpc.exe ...")
	if err := uploadWindowsFile(client, binary, "C:/ProgramData/frp/frpc.exe"); err != nil {
		emit("上传 frpc.exe 失败: " + err.Error())
		return node, err
	}
	emit("frpc.exe 已上传")

	if len(groups) == 0 {
		node.Installed = true
		node.Configured = false
		node.Status = "installed"
		node.Message = "frpc 已安装，等待关联本地代理"
		s.persistNode(node)
		emit("安装完成。请先在「配置本地代理」中关联此节点，再重新部署以创建后台任务")
		return node, nil
	}

	configs := make([]string, 0, len(groups))
	taskNames := make([]string, 0, len(groups))
	for _, group := range groups {
		suffix := groupUnitSuffix(group.Server.ID)
		cfgPath := "C:/ProgramData/frp/frpc-" + suffix + ".toml"
		if err := uploadWindowsFile(client, []byte(buildNodeFrpcConfig(group.Server, group.Proxies, "windows")), cfgPath); err != nil {
			emit("上传配置失败: " + err.Error())
			return node, err
		}
		configs = append(configs, "C:\\ProgramData\\frp\\frpc-"+suffix+".toml")
		taskNames = append(taskNames, "IntranetPenetrationFrpc-"+suffix)
	}
	emit("配置已上传")

	quoted := func(values []string) string {
		parts := make([]string, len(values))
		for i, value := range values {
			parts[i] = "'" + strings.ReplaceAll(value, "'", "''") + "'"
		}
		return strings.Join(parts, ",")
	}

	script := fmt.Sprintf(`
$ErrorActionPreference = 'Stop'
$bin = 'C:\ProgramData\frp\frpc.exe'
# 清理旧的单实例任务。
if (Get-ScheduledTask -TaskName 'frpc' -ErrorAction SilentlyContinue) {
  Stop-ScheduledTask -TaskName 'frpc' -ErrorAction SilentlyContinue
  Unregister-ScheduledTask -TaskName 'frpc' -Confirm:$false
}
$configs = @(%s)
$names = @(%s)
$settings = New-ScheduledTaskSettingsSet -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries -RestartCount 3 -RestartInterval (New-TimeSpan -Minutes 1)
$trigger = New-ScheduledTaskTrigger -AtStartup
$principal = New-ScheduledTaskPrincipal -UserId 'SYSTEM' -LogonType ServiceAccount -RunLevel Highest
for ($i = 0; $i -lt $configs.Count; $i++) {
  $taskName = $names[$i]
  if (Get-ScheduledTask -TaskName $taskName -ErrorAction SilentlyContinue) {
    Stop-ScheduledTask -TaskName $taskName -ErrorAction SilentlyContinue
    Unregister-ScheduledTask -TaskName $taskName -Confirm:$false
  }
  $action = New-ScheduledTaskAction -Execute $bin -Argument ('-c "{0}"' -f $configs[$i])
  Register-ScheduledTask -TaskName $taskName -Action $action -Trigger $trigger -Principal $principal -Settings $settings -Force | Out-Null
  Start-ScheduledTask -TaskName $taskName
}
Start-Sleep -Seconds 2
$allRunning = $true
foreach ($taskName in $names) {
  $state = (Get-ScheduledTask -TaskName $taskName).State
  "task=$taskName state=$state"
  if ($state -ne 'Running') { $allRunning = $false }
}
"allrunning=$($allRunning.ToString().ToLower())"
if (Test-Path $bin) { "version=" + (& $bin -v) } else { "version=" }
`, quoted(configs), quoted(taskNames))

	out, err := runPowerShell(client, script)
	if err != nil {
		emit("创建计划任务失败: " + strings.TrimSpace(out))
		return node, fmt.Errorf("创建 Windows 计划任务失败（请确认 SSH 用户具有管理员权限）: %w", err)
	}
	values := parseKeyValues(out)
	node.Installed = true
	node.Configured = true
	if values["allrunning"] == "true" {
		node.Status = "active"
	} else {
		node.Status = "inactive"
	}
	node.Message = strings.TrimSpace(values["version"])
	s.persistNode(node)
	emit(fmt.Sprintf("部署完成，创建了 %d 个 frpc 实例，状态: %s", len(groups), node.Status))
	return node, nil
}

func uploadWindowsFile(client *ssh.Client, data []byte, remotePath string) error {
	if err := uploadFileSFTP(client, data, remotePath); err == nil {
		return nil
	}
	return uploadFileSFTP(client, data, "/"+remotePath)
}

// StartNodeFrpc 启动节点上的 frpc 服务。
func (s *ProxyService) StartNodeFrpc(nodeID string) (ClientNode, error) {
	return s.systemctlNode(nodeID, "start")
}

// StopNodeFrpc 停止节点上的 frpc 服务。
func (s *ProxyService) StopNodeFrpc(nodeID string) (ClientNode, error) {
	return s.systemctlNode(nodeID, "stop")
}

// GetNodeRuntimeLogs 读取节点上 frpc 的运行日志。
func (s *ProxyService) GetNodeRuntimeLogs(nodeID string) (string, error) {
	node, err := s.getNode(nodeID)
	if err != nil {
		return "", err
	}
	if node.AuthType == "local" {
		dir := localNodeDir(s)
		logFiles, _ := filepath.Glob(filepath.Join(dir, "frpc*.log"))
		lines := make([]string, 0)
		for _, logPath := range logFiles {
			data, err := os.ReadFile(logPath)
			if err != nil {
				continue
			}
			lines = append(lines, strings.Split(strings.TrimRight(string(data), "\n"), "\n")...)
		}
		if len(lines) == 0 {
			return "", errors.New("还没有 frpc 运行日志")
		}
		if len(lines) > 150 {
			lines = lines[len(lines)-150:]
		}
		return strings.Join(lines, "\n"), nil
	}

	client, err := dialSSH(nodeSSHTarget(node))
	if err != nil {
		return "", err
	}
	defer client.Close()

	osName := node.OS
	if osName == "" {
		osName, err = detectRemoteOS(client)
		if err != nil {
			return "", err
		}
	}

	switch osName {
	case "windows":
		return runPowerShell(client, `Get-Content -Path 'C:\ProgramData\frp\frpc.log' -Tail 150 -ErrorAction SilentlyContinue`)
	case "darwin":
		prefix, stdin, err := sudoContext(client, node.AuthType, node.Password)
		if err != nil {
			return "", err
		}
		return runRemoteWithStdin(client, prefix+"tail -n 150 /var/log/frpc*.log 2>/dev/null || true", stdin)
	default:
		prefix, stdin, err := sudoContext(client, node.AuthType, node.Password)
		if err != nil {
			return "", err
		}
		units := s.nodeServiceUnits(nodeID)
		command := "journalctl"
		for _, unit := range units {
			command += " -u " + unit
		}
		return runRemoteWithStdin(client, prefix+command+" -n 150 --no-pager 2>/dev/null || true", stdin)
	}
}

// GetNodeDeployLogs 返回本次运行期间节点部署日志。
func (s *ProxyService) GetNodeDeployLogs(nodeID string) []string {
	s.nodeLogsMu.Lock()
	defer s.nodeLogsMu.Unlock()
	logs := s.nodeLogs[nodeID]
	out := make([]string, len(logs))
	copy(out, logs)
	return out
}

func (s *ProxyService) nodeServerIDs(nodeID string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensureLoaded(); err != nil {
		return nil
	}
	seen := make(map[string]bool)
	ids := make([]string, 0)
	for _, proxy := range s.cfg.LocalProxies {
		if proxy.NodeID == nodeID && proxy.ServerID != "" && !seen[proxy.ServerID] {
			seen[proxy.ServerID] = true
			ids = append(ids, proxy.ServerID)
		}
	}
	return ids
}

// nodeServiceUnits 返回节点上按代理服务器拆分的 frpc 服务名。
func (s *ProxyService) nodeServiceUnits(nodeID string) []string {
	ids := s.nodeServerIDs(nodeID)
	if len(ids) == 0 {
		return []string{"frpc"}
	}
	units := make([]string, 0, len(ids))
	for _, id := range ids {
		units = append(units, "frpc-"+groupUnitSuffix(id))
	}
	return units
}

func (s *ProxyService) systemctlNode(nodeID, action string) (ClientNode, error) {
	node, err := s.getNode(nodeID)
	if err != nil {
		return node, err
	}

	if node.AuthType == "local" {
		dir := localNodeDir(s)
		binPath := filepath.Join(dir, "frpc.exe")
		configs := localConfigFiles(dir)
		if len(configs) == 0 {
			return node, errors.New("节点尚未关联本地代理，请先挂载本地代理并重新部署")
		}
		if action == "stop" {
			if err := localStopProcess(binPath); err != nil {
				return node, fmt.Errorf("停止本机 frpc 失败: %s", err.Error())
			}
			node.Status = "inactive"
			node.Message = "本机 frpc 已停止"
		} else {
			if err := localStartAll(binPath, configs); err != nil {
				return node, fmt.Errorf("启动本机 frpc 失败: %w", err)
			}
			node.Status = "active"
			node.Message = fmt.Sprintf("本机 frpc 已启动（%d 个实例）", len(configs))
		}
		s.persistNode(node)
		return node, nil
	}

	client, err := dialSSH(nodeSSHTarget(node))
	if err != nil {
		return node, err
	}
	defer client.Close()

	osName := node.OS
	if osName == "" {
		osName, err = detectRemoteOS(client)
		if err != nil {
			return node, err
		}
		node.OS = osName
	}

	units := s.nodeServiceUnits(nodeID)
	labels := make([]string, 0, len(units))
	for _, unit := range units {
		labels = append(labels, "com.frp."+unit)
	}

	switch osName {
	case "windows":
		tasks := make([]string, 0, len(units))
		for _, unit := range units {
			if unit == "frpc" {
				tasks = append(tasks, "frpc")
			} else {
				tasks = append(tasks, "IntranetPenetrationFrpc-"+strings.TrimPrefix(unit, "frpc-"))
			}
		}
		if len(tasks) == 0 {
			tasks = []string{"frpc"}
		}
		quoted := make([]string, len(tasks))
		for i, task := range tasks {
			quoted[i] = "'" + escapePowerShell(task) + "'"
		}
		command := "Start-ScheduledTask"
		if action == "stop" {
			command = "Stop-ScheduledTask"
		}
		script := fmt.Sprintf(`$ErrorActionPreference = 'Stop'
$names = @(%s)
foreach ($name in $names) {
  if (Get-ScheduledTask -TaskName $name -ErrorAction SilentlyContinue) { %s -TaskName $name }
}
Start-Sleep -Seconds 1
$running = 0
foreach ($name in $names) {
  $task = Get-ScheduledTask -TaskName $name -ErrorAction SilentlyContinue
  if ($task -and $task.State -eq 'Running') { $running++ }
}
"running=$running"
`, strings.Join(quoted, ","), command)
		out, err := runPowerShell(client, script)
		if err != nil {
			return node, fmt.Errorf("操作计划任务失败: %s", strings.TrimSpace(out))
		}
		if parseKeyValues(out)["running"] != "0" && parseKeyValues(out)["running"] != "" {
			node.Status = "active"
		} else {
			node.Status = "inactive"
		}
		node.Message = fmt.Sprintf("计划任务 %s（%d 个实例）", action, len(tasks))
	case "darwin":
		prefix, stdin, err := sudoContext(client, node.AuthType, node.Password)
		if err != nil {
			return node, err
		}
		var builder strings.Builder
		for _, label := range labels {
			plist := "/Library/LaunchDaemons/" + label + ".plist"
			if action == "start" {
				fmt.Fprintf(&builder, "launchctl bootout system '%s' 2>/dev/null || true\nlaunchctl bootstrap system '%s' 2>/dev/null || true\nlaunchctl kickstart -k system/%s\n", plist, plist, label)
			} else {
				fmt.Fprintf(&builder, "launchctl bootout system '%s' 2>/dev/null || true\n", plist)
			}
		}
		if builder.Len() > 0 {
			if err := runStreaming(client, prefix+builder.String(), stdin, func(line string) {
				s.appendNodeLog(nodeID, line)
			}); err != nil {
				return node, err
			}
		}
		active := 0
		for _, label := range labels {
			out, _ := runRemote(client, "launchctl print system/"+label+" >/dev/null 2>&1 && echo active || echo inactive")
			if strings.TrimSpace(out) == "active" {
				active++
			}
		}
		if active > 0 {
			node.Status = "active"
		} else {
			node.Status = "inactive"
		}
		node.Message = fmt.Sprintf("launchctl %s（%d 个实例）", action, len(labels))
	default:
		prefix, stdin, err := sudoContext(client, node.AuthType, node.Password)
		if err != nil {
			return node, err
		}
		if err := runStreaming(client, prefix+"systemctl "+action+" "+strings.Join(units, " "), stdin, func(line string) {
			s.appendNodeLog(nodeID, line)
		}); err != nil {
			return node, err
		}
		active := 0
		for _, unit := range units {
			out, _ := runRemote(client, "systemctl is-active "+unit+" 2>/dev/null")
			if strings.TrimSpace(out) == "active" {
				active++
			}
		}
		if active > 0 {
			node.Status = "active"
		} else {
			node.Status = "inactive"
		}
		node.Message = fmt.Sprintf("systemctl %s（%d 个实例）", action, len(units))
	}

	s.persistNode(node)
	return node, nil
}

func (s *ProxyService) getNode(nodeID string) (ClientNode, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensureLoaded(); err != nil {
		return ClientNode{}, err
	}
	for _, item := range s.cfg.Nodes {
		if item.ID == nodeID {
			return item, nil
		}
	}
	return ClientNode{}, errors.New("客户端节点不存在")
}

func (s *ProxyService) persistNode(node ClientNode) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensureLoaded(); err != nil {
		return
	}
	for i := range s.cfg.Nodes {
		if s.cfg.Nodes[i].ID == node.ID {
			s.cfg.Nodes[i].OS = node.OS
			s.cfg.Nodes[i].Installed = node.Installed
			s.cfg.Nodes[i].Configured = node.Configured
			s.cfg.Nodes[i].Status = node.Status
			s.cfg.Nodes[i].Message = node.Message
			_ = s.persist()
			break
		}
	}
}

func (s *ProxyService) appendNodeLog(nodeID, line string) {
	s.nodeLogsMu.Lock()
	logs := append(s.nodeLogs[nodeID], line)
	if len(logs) > nodeMaxLogLines {
		logs = logs[len(logs)-nodeMaxLogLines:]
	}
	s.nodeLogs[nodeID] = logs
	s.nodeLogsMu.Unlock()

	if app := application.Get(); app != nil {
		app.Event.Emit("node:deploy:log", NodeDeployLogEvent{NodeID: nodeID, Line: line})
	}
}

func linuxFrpPlatform(machine string) (frpPlatformInfo, error) {
	switch machine {
	case "x86_64", "amd64":
		return frpPlatformInfo{os: "linux", arch: "amd64"}, nil
	case "aarch64", "arm64":
		return frpPlatformInfo{os: "linux", arch: "arm64"}, nil
	case "armv7l", "armv7", "armhf":
		return frpPlatformInfo{os: "linux", arch: "arm"}, nil
	case "i386", "i686":
		// frp 官方已不再发布 32 位 Linux 构建，先尝试 amd64 静态版本。
		return frpPlatformInfo{os: "linux", arch: "amd64"}, nil
	default:
		return frpPlatformInfo{}, fmt.Errorf("不支持的 CPU 架构: %s", machine)
	}
}

func buildNodeFrpcConfig(server ProxyServer, proxies []LocalProxy, osName string) string {
	var builder strings.Builder
	fmt.Fprintf(&builder, "serverAddr = %q\nserverPort = %d\n\nauth.method = \"token\"\nauth.token = %q\n\n", server.Host, server.FrpsPort, server.Token)
	if osName == "windows" {
		builder.WriteString("log.to = \"file\"\nlog.path = \"C:/ProgramData/frp/frpc.log\"\nlog.maxDays = 3\n")
	} else {
		builder.WriteString("log.to = \"console\"\nlog.level = \"info\"\n")
	}
	for _, proxy := range proxies {
		localIP := strings.TrimSpace(proxy.Host)
		if localIP == "" {
			localIP = "127.0.0.1"
		}
		fmt.Fprintf(&builder, "\n[[proxies]]\nname = %q\ntype = \"tcp\"\nlocalIP = %q\nlocalPort = %d\nremotePort = %d\n", "proxy-"+proxy.ID, localIP, proxy.Port, proxy.RemotePort)
	}
	return builder.String()
}

func sudoContext(client *ssh.Client, authType, password string) (string, string, error) {
	if uid, _ := runRemote(client, "id -u"); strings.TrimSpace(uid) == "0" {
		return "", "", nil
	}
	if _, err := runRemote(client, "sudo -n true"); err == nil {
		return "sudo -n ", "", nil
	}
	if authType != "key" && password != "" {
		return "sudo -S -p '' ", password + "\n", nil
	}
	return "", "", errors.New("当前 SSH 用户不是 root，且无法免密 sudo。请使用 root 或配置 NOPASSWD sudo")
}

func uploadBytes(client *ssh.Client, data []byte, remotePath string) error {
	session, err := client.NewSession()
	if err != nil {
		return err
	}
	defer session.Close()
	session.Stdin = bytes.NewReader(data)
	return session.Run("cat > '" + remotePath + "'")
}

func runRemoteWithStdin(client *ssh.Client, command, stdin string) (string, error) {
	session, err := client.NewSession()
	if err != nil {
		return "", err
	}
	defer session.Close()

	var out bytes.Buffer
	session.Stdout = &out
	session.Stderr = &out
	if stdin != "" {
		session.Stdin = strings.NewReader(stdin)
	}
	err = session.Run(command)
	return strings.TrimSpace(out.String()), err
}
