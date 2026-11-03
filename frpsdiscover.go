package main

import (
	"fmt"
	"strconv"
	"strings"
)

// FrpsDiscovery 描述远程服务器上已存在的 frps 安装与配置。
type FrpsDiscovery struct {
	Installed  bool   `json:"installed"`
	Version    string `json:"version"`
	Status     string `json:"status"`
	ConfigPath string `json:"configPath"`
	BindPort   int    `json:"bindPort"`
	Token      string `json:"token"`
	Message    string `json:"message"`
}

// DiscoverFrps 发现服务器上已有的 frps 安装与配置，不写入任何文件。
// 用于避免同一台服务器被不同 SSH 用户重复安装而覆盖现有配置。
func (s *ProxyService) DiscoverFrps(server ProxyServer) (FrpsDiscovery, error) {
	if err := validateConnection(server); err != nil {
		return FrpsDiscovery{}, err
	}
	client, err := dialSSH(server)
	if err != nil {
		return FrpsDiscovery{}, err
	}
	defer client.Close()

	result := FrpsDiscovery{}

	versionCmd := "if [ -x /opt/frp/frps ]; then /opt/frp/frps -v 2>/dev/null; elif command -v frps >/dev/null 2>&1; then frps -v 2>/dev/null; fi"
	if out, _ := runRemote(client, versionCmd); strings.TrimSpace(out) != "" {
		result.Version = strings.TrimSpace(out)
	}
	if out, _ := runRemote(client, "systemctl is-active frps 2>/dev/null || true"); strings.TrimSpace(out) != "" {
		result.Status = strings.TrimSpace(out)
	}

	// 从 systemd 单元读取实际使用的配置文件，兼容非 /etc/frp 的自定义路径。
	unitCmd := `for f in /etc/systemd/system/frps.service /lib/systemd/system/frps.service /usr/lib/systemd/system/frps.service; do
  if [ -f "$f" ]; then
    awk '/^ExecStart=/{for(i=1;i<=NF;i++) if($i=="-c"){print $(i+1); exit}}' "$f"
    break
  fi
done`
	candidates := make([]string, 0, 5)
	if out, _ := runRemote(client, unitCmd); strings.TrimSpace(out) != "" {
		candidates = append(candidates, strings.TrimSpace(out))
	}
	candidates = append(candidates, "/etc/frp/frps.toml", "/etc/frp/frps.ini", "/opt/frp/frps.toml", "/opt/frp/frps.ini")

	// 配置权限通常是 600，需要 root 或 sudo 才能读取。
	prefix, stdin, sudoErr := sudoContext(client, server.AuthType, server.Password)
	if sudoErr != nil {
		prefix, stdin = "", ""
	}

	seen := make(map[string]bool)
	for _, path := range candidates {
		path = strings.TrimSpace(path)
		if path == "" || seen[path] {
			continue
		}
		seen[path] = true
		out, err := runRemoteWithStdin(client, prefix+"cat '"+path+"' 2>/dev/null", stdin)
		if err != nil || strings.TrimSpace(out) == "" {
			continue
		}
		result.ConfigPath = path
		result.BindPort, result.Token = parseFrpsConfig(out)
		break
	}

	result.Installed = result.Version != "" || result.Status == "active" || result.ConfigPath != ""

	parts := make([]string, 0, 5)
	if result.Installed {
		if result.Version != "" {
			parts = append(parts, "已安装 "+result.Version)
		} else {
			parts = append(parts, "已安装 frps")
		}
		if result.Status != "" {
			parts = append(parts, "服务状态: "+result.Status)
		}
	} else {
		parts = append(parts, "未发现已安装的 frps")
	}
	if result.ConfigPath != "" {
		parts = append(parts, "配置: "+result.ConfigPath)
		if result.BindPort > 0 {
			parts = append(parts, fmt.Sprintf("端口 %d", result.BindPort))
		}
		if result.Token != "" {
			parts = append(parts, "已读取 Token")
		}
	} else if result.Installed && sudoErr != nil {
		parts = append(parts, "无法读取配置（需要 sudo 权限）")
	}
	result.Message = strings.Join(parts, " · ")

	return result, nil
}

// parseFrpsConfig 同时兼容 TOML（bindPort / auth.token）与 INI（bind_port / token）。
func parseFrpsConfig(content string) (int, string) {
	port := 0
	token := ""
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.ToLower(strings.TrimSpace(key))
		value = strings.Trim(strings.TrimSpace(value), `"'`)
		switch key {
		case "bindport", "bind_port":
			if parsed, err := strconv.Atoi(value); err == nil {
				port = parsed
			}
		case "auth.token", "token":
			if token == "" {
				token = value
			}
		}
	}
	return port, token
}
