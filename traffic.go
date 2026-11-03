package main

import (
	"bytes"
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"strconv"
	"strings"

	"golang.org/x/crypto/ssh"
)

// TrafficStats 表示一次约 1 秒采样的网络流量。
type TrafficStats struct {
	RxRate  int64 `json:"rxRate"`
	TxRate  int64 `json:"txRate"`
	RxTotal int64 `json:"rxTotal"`
	TxTotal int64 `json:"txTotal"`
}

const unixTrafficLinux = `echo __SAMPLE1__; cat /proc/net/dev 2>/dev/null; sleep 1; echo __SAMPLE2__; cat /proc/net/dev 2>/dev/null`
const unixTrafficDarwin = `echo __SAMPLE1__; netstat -ibn 2>/dev/null; sleep 1; echo __SAMPLE2__; netstat -ibn 2>/dev/null`
const windowsTrafficScript = `
$a = (Get-NetAdapterStatistics | Measure-Object -Property ReceivedBytes -Sum).Sum
$b = (Get-NetAdapterStatistics | Measure-Object -Property SentBytes -Sum).Sum
Start-Sleep -Seconds 1
$c = (Get-NetAdapterStatistics | Measure-Object -Property ReceivedBytes -Sum).Sum
$d = (Get-NetAdapterStatistics | Measure-Object -Property SentBytes -Sum).Sum
"rx1=$a"
"tx1=$b"
"rx2=$c"
"tx2=$d"
`

// GetServerTraffic 采样公网服务器的上传/下载速率。
func (s *ProxyService) GetServerTraffic(server ProxyServer) (TrafficStats, error) {
	client, err := dialSSH(server)
	if err != nil {
		return TrafficStats{}, err
	}
	defer client.Close()

	osName, err := detectRemoteOS(client)
	if err != nil {
		return TrafficStats{}, err
	}
	return sampleTrafficSSH(client, osName)
}

// GetNodeTraffic 采样客户端节点的上传/下载速率。
func (s *ProxyService) GetNodeTraffic(nodeID string) (TrafficStats, error) {
	node, err := s.getNode(nodeID)
	if err != nil {
		return TrafficStats{}, err
	}
	if node.AuthType == "local" {
		return s.GetLocalTraffic()
	}

	client, err := dialSSH(nodeSSHTarget(node))
	if err != nil {
		return TrafficStats{}, err
	}
	defer client.Close()

	osName := node.OS
	if osName == "" {
		osName, err = detectRemoteOS(client)
		if err != nil {
			return TrafficStats{}, err
		}
	}
	return sampleTrafficSSH(client, osName)
}

// GetLocalProxyTraffic 采样本地代理所在机器的网络速率。
func (s *ProxyService) GetLocalProxyTraffic(proxyID string) (TrafficStats, error) {
	s.mu.Lock()
	var proxy LocalProxy
	found := false
	if err := s.ensureLoaded(); err == nil {
		for _, item := range s.cfg.LocalProxies {
			if item.ID == proxyID {
				proxy = item
				found = true
				break
			}
		}
	}
	s.mu.Unlock()

	if !found {
		return TrafficStats{}, errors.New("本地代理不存在")
	}
	if proxy.NodeID != "" {
		return s.GetNodeTraffic(proxy.NodeID)
	}
	return s.GetLocalTraffic()
}

// GetLocalTraffic 采样当前桌面程序所在机器的网络速率。
func (s *ProxyService) GetLocalTraffic() (TrafficStats, error) {
	switch runtime.GOOS {
	case "windows":
		out, err := runLocalCommand("powershell", "-NoProfile", "-ExecutionPolicy", "Bypass", "-Command", windowsTrafficScript)
		if err != nil {
			return TrafficStats{}, err
		}
		return parseWindowsTraffic(out)
	case "darwin":
		out, err := runLocalCommand("sh", "-c", unixTrafficDarwin)
		if err != nil {
			return TrafficStats{}, err
		}
		return parseUnixTraffic(out, true)
	default:
		out, err := runLocalCommand("sh", "-c", unixTrafficLinux)
		if err != nil {
			return TrafficStats{}, err
		}
		return parseUnixTraffic(out, false)
	}
}

func sampleTrafficSSH(client *ssh.Client, osName string) (TrafficStats, error) {
	switch osName {
	case "windows":
		out, err := runPowerShell(client, windowsTrafficScript)
		if err != nil {
			return TrafficStats{}, err
		}
		return parseWindowsTraffic(out)
	case "darwin":
		out, err := runRemote(client, unixTrafficDarwin)
		if err != nil {
			return TrafficStats{}, err
		}
		return parseUnixTraffic(out, true)
	default:
		out, err := runRemote(client, unixTrafficLinux)
		if err != nil {
			return TrafficStats{}, err
		}
		return parseUnixTraffic(out, false)
	}
}

func runLocalCommand(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	hideCommandWindow(cmd)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	return out.String(), err
}

func parseWindowsTraffic(output string) (TrafficStats, error) {
	values := parseKeyValues(output)
	rx1, err1 := strconv.ParseInt(values["rx1"], 10, 64)
	tx1, err2 := strconv.ParseInt(values["tx1"], 10, 64)
	rx2, err3 := strconv.ParseInt(values["rx2"], 10, 64)
	tx2, err4 := strconv.ParseInt(values["tx2"], 10, 64)
	if err1 != nil || err2 != nil || err3 != nil || err4 != nil {
		return TrafficStats{}, fmt.Errorf("解析 Windows 流量数据失败: %q", strings.TrimSpace(output))
	}
	return buildTrafficStats(rx1, tx1, rx2, tx2), nil
}

func parseUnixTraffic(output string, darwin bool) (TrafficStats, error) {
	parts := strings.Split(output, "__SAMPLE2__")
	if len(parts) != 2 {
		return TrafficStats{}, fmt.Errorf("解析流量采样输出失败: %q", strings.TrimSpace(output))
	}

	var rx1, tx1, rx2, tx2 int64
	if darwin {
		rx1, tx1 = sumDarwinCounters(parts[0])
		rx2, tx2 = sumDarwinCounters(parts[1])
	} else {
		rx1, tx1 = sumLinuxCounters(parts[0])
		rx2, tx2 = sumLinuxCounters(parts[1])
	}
	if rx2 == 0 && tx2 == 0 {
		return TrafficStats{}, errors.New("未读取到网卡流量数据")
	}
	return buildTrafficStats(rx1, tx1, rx2, tx2), nil
}

func buildTrafficStats(rx1, tx1, rx2, tx2 int64) TrafficStats {
	rxRate := rx2 - rx1
	txRate := tx2 - tx1
	if rxRate < 0 {
		rxRate = 0
	}
	if txRate < 0 {
		txRate = 0
	}
	return TrafficStats{RxRate: rxRate, TxRate: txRate, RxTotal: rx2, TxTotal: tx2}
}

func sumLinuxCounters(section string) (rx, tx int64) {
	for _, line := range strings.Split(section, "\n") {
		if !strings.Contains(line, ":") {
			continue
		}
		parts := strings.SplitN(line, ":", 2)
		if strings.TrimSpace(parts[0]) == "lo" {
			continue
		}
		fields := strings.Fields(parts[1])
		if len(fields) < 9 {
			continue
		}
		r, err1 := strconv.ParseInt(fields[0], 10, 64)
		t, err2 := strconv.ParseInt(fields[8], 10, 64)
		if err1 == nil && err2 == nil {
			rx += r
			tx += t
		}
	}
	return rx, tx
}

func sumDarwinCounters(section string) (rx, tx int64) {
	for _, line := range strings.Split(section, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 10 {
			continue
		}
		name := fields[0]
		if name == "Name" || strings.HasPrefix(name, "lo") {
			continue
		}
		if fields[6] == "-" {
			continue
		}
		r, err1 := strconv.ParseInt(fields[6], 10, 64)
		t, err2 := strconv.ParseInt(fields[9], 10, 64)
		if err1 == nil && err2 == nil {
			rx += r
			tx += t
		}
	}
	return rx, tx
}
