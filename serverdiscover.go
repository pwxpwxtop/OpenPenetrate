package main

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// 服务器端缓存穿透配置的目录，多个客户端/用户可共享发现。
const remoteProxyConfigDir = "/etc/frp/intranet-penetration/proxies"

// PortInfo 描述服务器上一个正在监听的 TCP 端口。
type PortInfo struct {
	Port    int    `json:"port"`
	Process string `json:"process"`
}

// RemoteTunnelConfig 是缓存在代理服务器上的穿透配置（不含密码与 Token）。
type RemoteTunnelConfig struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	LocalHost  string `json:"localHost"`
	LocalPort  int    `json:"localPort"`
	RemotePort int    `json:"remotePort"`
	NodeID     string `json:"nodeId"`
	Owner      string `json:"owner"`
	UpdatedAt  string `json:"updatedAt"`
}

// ServerServiceDiscovery 是代理服务器监听端口与缓存配置的发现结果。
type ServerServiceDiscovery struct {
	Host    string               `json:"host"`
	Ports   []PortInfo           `json:"ports"`
	Configs []RemoteTunnelConfig `json:"configs"`
}

// DiscoverServerServices 读取服务器监听端口，并读取服务器上缓存的穿透配置。
func (s *ProxyService) DiscoverServerServices(server ProxyServer) (ServerServiceDiscovery, error) {
	if err := validateConnection(server); err != nil {
		return ServerServiceDiscovery{}, err
	}
	client, err := dialSSH(server)
	if err != nil {
		return ServerServiceDiscovery{}, err
	}
	defer client.Close()

	prefix, stdin, sudoErr := sudoContext(client, server.AuthType, server.Password)
	if sudoErr != nil {
		prefix, stdin = "", ""
	}

	result := ServerServiceDiscovery{Host: server.Host, Ports: []PortInfo{}, Configs: []RemoteTunnelConfig{}}

	if _, err := runRemote(client, "command -v ss >/dev/null 2>&1 || command -v netstat >/dev/null 2>&1"); err != nil {
		return result, errors.New("服务器缺少 ss 或 netstat，无法读取监听端口")
	}

	// 非 root 用户也能列出端口；有 sudo 时额外获取进程名。
	portCmd := prefix + `sh -c 'if command -v ss >/dev/null 2>&1; then ss -H -ltnp 2>/dev/null; elif command -v netstat >/dev/null 2>&1; then netstat -ltnp 2>/dev/null; fi'`
	portOut, err := runRemoteWithStdin(client, portCmd, stdin)
	if err != nil && strings.TrimSpace(portOut) == "" {
		return result, fmt.Errorf("读取监听端口失败: %w", err)
	}
	result.Ports = parseListeningPorts(portOut)

	// 配置目录可能不存在，读取失败时仅返回空列表，不影响端口发现。
	configCmd := prefix + `sh -c 'd="` + remoteProxyConfigDir + `"; [ -d "$d" ] || exit 0; for f in "$d"/*.json; do [ -f "$f" ] || continue; echo "__FILE__$f"; cat "$f" 2>/dev/null; echo; done'`
	configOut, _ := runRemoteWithStdin(client, configCmd, stdin)
	result.Configs = parseRemoteTunnelConfigs(configOut)

	return result, nil
}

// syncProxyConfigToServer 将穿透配置缓存到代理服务器，供其他客户端发现。
func (s *ProxyService) syncProxyConfigToServer(server ProxyServer, proxy LocalProxy) {
	client, err := dialSSH(server)
	if err != nil {
		return
	}
	defer client.Close()

	prefix, stdin, err := sudoContext(client, server.AuthType, server.Password)
	if err != nil {
		return
	}

	config := RemoteTunnelConfig{
		ID:         proxy.ID,
		Name:       proxy.Name,
		LocalHost:  proxy.Host,
		LocalPort:  proxy.Port,
		RemotePort: proxy.RemotePort,
		NodeID:     proxy.NodeID,
		Owner:      server.Username,
		UpdatedAt:  time.Now().Format(time.RFC3339),
	}
	data, err := json.Marshal(config)
	if err != nil {
		return
	}
	encoded := base64.StdEncoding.EncodeToString(data)
	path := fmt.Sprintf("%s/%s.json", remoteProxyConfigDir, proxy.ID)
	command := prefix + fmt.Sprintf("mkdir -p '%s' && printf '%%s' '%s' | base64 -d > '%s'", remoteProxyConfigDir, encoded, path)
	_, _ = runRemoteWithStdin(client, command, stdin)
}

// removeProxyConfigFromServer 删除服务器上缓存的穿透配置。
func (s *ProxyService) removeProxyConfigFromServer(server ProxyServer, proxyID string) {
	client, err := dialSSH(server)
	if err != nil {
		return
	}
	defer client.Close()

	prefix, stdin, err := sudoContext(client, server.AuthType, server.Password)
	if err != nil {
		return
	}
	path := fmt.Sprintf("%s/%s.json", remoteProxyConfigDir, proxyID)
	_, _ = runRemoteWithStdin(client, prefix+"rm -f '"+path+"'", stdin)
}

func parseRemoteTunnelConfigs(output string) []RemoteTunnelConfig {
	configs := make([]RemoteTunnelConfig, 0)
	lines := strings.Split(output, "\n")
	for i := 0; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		if !strings.HasPrefix(line, "__FILE__") {
			continue
		}
		for j := i + 1; j < len(lines); j++ {
			data := strings.TrimSpace(lines[j])
			if data == "" {
				continue
			}
			var config RemoteTunnelConfig
			if err := json.Unmarshal([]byte(data), &config); err == nil && config.ID != "" {
				configs = append(configs, config)
			}
			i = j
			break
		}
	}
	sort.Slice(configs, func(i, j int) bool { return configs[i].RemotePort < configs[j].RemotePort })
	return configs
}

func parseListeningPorts(output string) []PortInfo {
	seen := make(map[int]string)
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}

		address := ""
		process := ""
		switch {
		case fields[0] == "LISTEN":
			// ss -H -ltnp 输出
			address = fields[3]
			process = parseSSProcess(line)
		case strings.HasPrefix(fields[0], "tcp"):
			// netstat -ltnp 输出
			address = fields[3]
			if len(fields) > 6 {
				process = parseNetstatProcess(fields[6])
			}
		default:
			continue
		}

		port := portFromAddress(address)
		if port <= 0 {
			continue
		}
		// IPv4 / IPv6 会重复出现同一端口，只保留一条。
		if existing, ok := seen[port]; !ok || existing == "" {
			seen[port] = process
		}
	}

	ports := make([]PortInfo, 0, len(seen))
	for port, process := range seen {
		ports = append(ports, PortInfo{Port: port, Process: process})
	}
	sort.Slice(ports, func(i, j int) bool { return ports[i].Port < ports[j].Port })
	return ports
}

func portFromAddress(address string) int {
	index := strings.LastIndex(address, ":")
	if index < 0 || index == len(address)-1 {
		return 0
	}
	port, err := strconv.Atoi(address[index+1:])
	if err != nil {
		return 0
	}
	return port
}

func parseSSProcess(line string) string {
	const marker = "users:(("
	index := strings.Index(line, marker)
	if index < 0 {
		return ""
	}
	rest := line[index+len(marker):]
	start := strings.Index(rest, "\"")
	if start < 0 {
		return ""
	}
	rest = rest[start+1:]
	end := strings.Index(rest, "\"")
	if end < 0 {
		return ""
	}
	return rest[:end]
}

func parseNetstatProcess(field string) string {
	if index := strings.Index(field, "/"); index >= 0 {
		return field[index+1:]
	}
	return ""
}
