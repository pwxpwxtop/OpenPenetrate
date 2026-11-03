package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// LocalProxy 描述一个需要暴露到公网的内网服务。
type LocalProxy struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Host          string `json:"host"`
	Port          int    `json:"port"`
	RemotePort    int    `json:"remotePort"`
	ServerID      string `json:"serverId"`
	NodeID        string `json:"nodeId"`
	Status        string `json:"status"`
	Message       string `json:"message"`
	RemoteStatus  string `json:"remoteStatus"`
	RemoteMessage string `json:"remoteMessage"`
}

// ProxyServer 描述一台运行 frps 的公网服务器。
type ProxyServer struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Host        string `json:"host"`
	SSHPort     int    `json:"sshPort"`
	Username    string `json:"username"`
	AuthType    string `json:"authType"`
	Password    string `json:"password"`
	PrivateKey  string `json:"privateKey"`
	FrpsPort    int    `json:"frpsPort"`
	Token       string `json:"token"`
	Installed   bool   `json:"installed"`
	Status      string `json:"status"`
	PortStatus  string `json:"portStatus"`
	PortMessage string `json:"portMessage"`
}

// ClientNode 描述一台运行 frpc 的 Linux 客户端节点。
type ClientNode struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Host       string `json:"host"`
	SSHPort    int    `json:"sshPort"`
	Username   string `json:"username"`
	AuthType   string `json:"authType"`
	Password   string `json:"password"`
	PrivateKey string `json:"privateKey"`
	OS         string `json:"os"`
	Installed  bool   `json:"installed"`
	Configured bool   `json:"configured"`
	Status     string `json:"status"`
	Message    string `json:"message"`
}

type appConfig struct {
	LocalProxies []LocalProxy  `json:"localProxies"`
	Servers      []ProxyServer `json:"servers"`
	Nodes        []ClientNode  `json:"nodes"`
}

// ProxyService 负责配置的持久化，以及通过 SSH 管理公网服务器。
type ProxyService struct {
	mu     sync.Mutex
	dir    string
	file   string
	loaded bool
	cfg    appConfig

	frpcMu sync.Mutex
	frpc   map[string]*frpcProcess

	nodeLogsMu sync.Mutex
	nodeLogs   map[string][]string
}

func NewProxyService() *ProxyService {
	return &ProxyService{
		frpc:     make(map[string]*frpcProcess),
		nodeLogs: make(map[string][]string),
	}
}

func (s *ProxyService) ensureLoaded() error {
	if s.loaded {
		return nil
	}
	base, err := os.UserConfigDir()
	if err != nil {
		base = "."
	}
	s.dir = filepath.Join(base, "IntranetPenetration")
	s.file = filepath.Join(s.dir, "config.json")
	s.loaded = true

	data, err := os.ReadFile(s.file)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if len(data) == 0 {
		return nil
	}
	return json.Unmarshal(data, &s.cfg)
}

func (s *ProxyService) persist() error {
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s.cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.file, data, 0o600)
}

func (s *ProxyService) ListLocalProxies() ([]LocalProxy, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensureLoaded(); err != nil {
		return nil, err
	}
	list := make([]LocalProxy, len(s.cfg.LocalProxies))
	copy(list, s.cfg.LocalProxies)
	return list, nil
}

func (s *ProxyService) SaveLocalProxy(proxy LocalProxy) (LocalProxy, error) {
	proxy.Name = strings.TrimSpace(proxy.Name)
	proxy.Host = normalizeHost(proxy.Host)
	if proxy.Name == "" {
		return proxy, errors.New("名称不能为空")
	}
	if proxy.Host == "" {
		return proxy, errors.New("本地 IP 或域名不能为空")
	}
	if proxy.Port < 1 || proxy.Port > 65535 {
		return proxy, errors.New("端口必须在 1-65535 之间")
	}
	if proxy.RemotePort < 1 || proxy.RemotePort > 65535 {
		return proxy, errors.New("远程访问端口必须在 1-65535 之间")
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensureLoaded(); err != nil {
		return proxy, err
	}

	proxy.ServerID = strings.TrimSpace(proxy.ServerID)
	var syncTarget ProxyServer
	if proxy.ServerID != "" {
		foundServer := false
		for i := range s.cfg.Servers {
			if s.cfg.Servers[i].ID == proxy.ServerID {
				foundServer = true
				syncTarget = s.cfg.Servers[i]
				break
			}
		}
		if !foundServer {
			return proxy, errors.New("关联的代理服务器不存在，请重新选择")
		}
	}

	proxy.NodeID = strings.TrimSpace(proxy.NodeID)
	if proxy.NodeID != "" {
		foundNode := false
		for i := range s.cfg.Nodes {
			if s.cfg.Nodes[i].ID == proxy.NodeID {
				foundNode = true
				break
			}
		}
		if !foundNode {
			return proxy, errors.New("关联的客户端节点不存在，请重新选择")
		}
	}

	if proxy.ID == "" {
		proxy.ID = newID()
		proxy.Status = ""
		proxy.Message = ""
		proxy.RemoteStatus = ""
		proxy.RemoteMessage = ""
		s.cfg.LocalProxies = append(s.cfg.LocalProxies, proxy)
	} else {
		found := false
		for i := range s.cfg.LocalProxies {
			existing := s.cfg.LocalProxies[i]
			if existing.ID == proxy.ID {
				// 目标地址或端口变化后，之前的检测结果不再可信。
				if existing.Host != proxy.Host || existing.Port != proxy.Port {
					proxy.Status = ""
					proxy.Message = ""
				}
				if existing.ServerID != proxy.ServerID || existing.RemotePort != proxy.RemotePort {
					proxy.RemoteStatus = ""
					proxy.RemoteMessage = ""
				}
				s.cfg.LocalProxies[i] = proxy
				found = true
				break
			}
		}
		if !found {
			return proxy, errors.New("要编辑的本地代理不存在")
		}
	}

	if err := s.persist(); err != nil {
		return proxy, err
	}
	// 把穿透配置缓存到关联的代理服务器，供其他客户端发现。
	if syncTarget.ID != "" {
		go s.syncProxyConfigToServer(syncTarget, proxy)
	}
	return proxy, nil
}

// CheckLocalProxy 通过 TCP 探测本地服务是否可达，并记录检测结果。
func (s *ProxyService) CheckLocalProxy(proxy LocalProxy) (LocalProxy, error) {
	if strings.TrimSpace(proxy.Host) == "" {
		return proxy, errors.New("本地 IP 或域名不能为空")
	}
	if proxy.Port < 1 || proxy.Port > 65535 {
		return proxy, errors.New("端口必须在 1-65535 之间")
	}

	address := net.JoinHostPort(strings.TrimSpace(proxy.Host), strconv.Itoa(proxy.Port))
	started := time.Now()
	conn, err := net.DialTimeout("tcp", address, 5*time.Second)
	if err != nil {
		proxy.Status = "failed"
		proxy.Message = fmt.Sprintf("无法连接 %s: %v", address, err)
	} else {
		_ = conn.Close()
		proxy.Status = "ok"
		proxy.Message = fmt.Sprintf("本地服务可达（%d ms）", time.Since(started).Milliseconds())
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensureLoaded(); err != nil {
		return proxy, err
	}
	if proxy.ID != "" {
		for i := range s.cfg.LocalProxies {
			if s.cfg.LocalProxies[i].ID == proxy.ID {
				s.cfg.LocalProxies[i] = proxy
				if err := s.persist(); err != nil {
					return proxy, err
				}
				break
			}
		}
	}
	return proxy, nil
}

// CheckRemoteProxy 从本机探测公网服务器上的远程端口是否可连接。
func (s *ProxyService) CheckRemoteProxy(proxy LocalProxy) (LocalProxy, error) {
	if proxy.RemotePort < 1 || proxy.RemotePort > 65535 {
		return proxy, errors.New("远程访问端口必须在 1-65535 之间")
	}
	if strings.TrimSpace(proxy.ServerID) == "" {
		return proxy, errors.New("请先选择关联的代理服务器")
	}

	s.mu.Lock()
	if err := s.ensureLoaded(); err != nil {
		s.mu.Unlock()
		return proxy, err
	}
	var server ProxyServer
	serverFound := false
	for i := range s.cfg.Servers {
		if s.cfg.Servers[i].ID == proxy.ServerID {
			server = s.cfg.Servers[i]
			serverFound = true
			break
		}
	}
	s.mu.Unlock()
	if !serverFound {
		return proxy, errors.New("关联的代理服务器不存在，请重新选择")
	}

	address := net.JoinHostPort(server.Host, strconv.Itoa(proxy.RemotePort))
	started := time.Now()
	conn, err := net.DialTimeout("tcp", address, 5*time.Second)
	if err != nil {
		proxy.RemoteStatus = "failed"
		proxy.RemoteMessage = s.remoteFailureHint(proxy, server, address)
	} else {
		_ = conn.Close()
		proxy.RemoteStatus = "ok"
		proxy.RemoteMessage = fmt.Sprintf("公网端口可连接（%d ms）", time.Since(started).Milliseconds())
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.cfg.LocalProxies {
		if s.cfg.LocalProxies[i].ID == proxy.ID {
			s.cfg.LocalProxies[i] = proxy
			if err := s.persist(); err != nil {
				return proxy, err
			}
			break
		}
	}
	return proxy, nil
}

// remoteFailureHint 在远程端口不可达时给出可操作的诊断信息。
func (s *ProxyService) remoteFailureHint(proxy LocalProxy, server ProxyServer, remoteAddress string) string {
	frpsPort := server.FrpsPort
	if frpsPort == 0 {
		frpsPort = 7000
	}
	frpsAddress := net.JoinHostPort(server.Host, strconv.Itoa(frpsPort))
	if !tcpReachable(frpsAddress) {
		return fmt.Sprintf("远程端口 %s 不可连接：frps 端口 %s 也不可达，请先在「代理服务器」页面确认 frps 已安装并运行", remoteAddress, frpsAddress)
	}

	if proxy.NodeID != "" {
		node, err := s.getNode(proxy.NodeID)
		if err == nil {
			client, dialErr := dialSSH(nodeSSHTarget(node))
			if dialErr == nil {
				defer client.Close()
				activeCount, _ := runRemote(client, "systemctl list-units --type=service --state=active --no-legend 'frpc*' 2>/dev/null | wc -l")
				if strings.TrimSpace(activeCount) == "0" || strings.TrimSpace(activeCount) == "" {
					return fmt.Sprintf("远程端口 %s 不可连接：客户端节点「%s」上的 frpc 未运行，请到「服务器挂载」部署并启动", remoteAddress, node.Name)
				}
				return fmt.Sprintf("远程端口 %s 不可连接：节点 frpc 正在运行，但尚未注册该端口。请确认已重新部署，并查看节点日志中的 start proxy success", remoteAddress)
			}
		}
	}

	return fmt.Sprintf("远程端口 %s 不可连接：请确认客户端节点上的 frpc 已部署并运行，且代理服务器上的 frps 已启动", remoteAddress)
}

func tcpReachable(address string) bool {
	conn, err := net.DialTimeout("tcp", address, 5*time.Second)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

func (s *ProxyService) DeleteLocalProxy(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensureLoaded(); err != nil {
		return err
	}
	for i := range s.cfg.LocalProxies {
		if s.cfg.LocalProxies[i].ID == id {
			proxy := s.cfg.LocalProxies[i]
			s.cfg.LocalProxies = append(s.cfg.LocalProxies[:i], s.cfg.LocalProxies[i+1:]...)
			if err := s.persist(); err != nil {
				return err
			}
			// 同步删除服务器上缓存的穿透配置。
			for _, server := range s.cfg.Servers {
				if server.ID == proxy.ServerID {
					go s.removeProxyConfigFromServer(server, id)
					break
				}
			}
			return nil
		}
	}
	return errors.New("本地代理不存在")
}

func (s *ProxyService) ListServers() ([]ProxyServer, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensureLoaded(); err != nil {
		return nil, err
	}
	list := make([]ProxyServer, len(s.cfg.Servers))
	copy(list, s.cfg.Servers)
	return list, nil
}

func (s *ProxyService) SaveServer(server ProxyServer) (ProxyServer, error) {
	server.Name = strings.TrimSpace(server.Name)
	server.Host = normalizeHost(server.Host)
	server.Username = strings.TrimSpace(server.Username)
	server.AuthType = strings.ToLower(strings.TrimSpace(server.AuthType))
	if server.AuthType == "" {
		server.AuthType = "password"
	}
	if server.SSHPort == 0 {
		server.SSHPort = 22
	}
	if server.FrpsPort == 0 {
		server.FrpsPort = 7000
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensureLoaded(); err != nil {
		return server, err
	}

	// 编辑时如果密码/私钥留空，沿用已保存的凭据。
	if server.ID != "" {
		for _, existing := range s.cfg.Servers {
			if existing.ID == server.ID {
				if server.Password == "" {
					server.Password = existing.Password
				}
				if server.PrivateKey == "" {
					server.PrivateKey = existing.PrivateKey
				}
				break
			}
		}
	}
	if server.Token == "" {
		server.Token = newToken()
	}

	if server.Name == "" {
		return server, errors.New("名称不能为空")
	}
	if server.Host == "" {
		return server, errors.New("公网服务器地址不能为空")
	}
	if server.Username == "" {
		return server, errors.New("SSH 用户名不能为空")
	}
	if server.SSHPort < 1 || server.SSHPort > 65535 {
		return server, errors.New("SSH 端口必须在 1-65535 之间")
	}
	if server.FrpsPort < 1 || server.FrpsPort > 65535 {
		return server, errors.New("frps 端口必须在 1-65535 之间")
	}
	switch server.AuthType {
	case "key":
		if strings.TrimSpace(server.PrivateKey) == "" {
			return server, errors.New("请填写 SSH 私钥")
		}
	case "password":
		if server.Password == "" {
			return server, errors.New("请填写 SSH 密码")
		}
	default:
		return server, errors.New("不支持的认证方式")
	}

	if server.ID == "" {
		server.ID = newID()
		server.PortStatus = ""
		server.PortMessage = ""
		s.cfg.Servers = append(s.cfg.Servers, server)
	} else {
		found := false
		for i := range s.cfg.Servers {
			if s.cfg.Servers[i].ID == server.ID {
				// 地址或端口变化后，之前的端口检测结果不再可信。
				if s.cfg.Servers[i].Host != server.Host || s.cfg.Servers[i].FrpsPort != server.FrpsPort {
					server.PortStatus = ""
					server.PortMessage = ""
				}
				s.cfg.Servers[i] = server
				found = true
				break
			}
		}
		if !found {
			return server, errors.New("要编辑的代理服务器不存在")
		}
	}

	if err := s.persist(); err != nil {
		return server, err
	}
	return server, nil
}

func (s *ProxyService) DeleteServer(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensureLoaded(); err != nil {
		return err
	}
	for i := range s.cfg.Servers {
		if s.cfg.Servers[i].ID == id {
			s.cfg.Servers = append(s.cfg.Servers[:i], s.cfg.Servers[i+1:]...)
			// 清理本地代理上已失效的关联和远程检测结果。
			for j := range s.cfg.LocalProxies {
				if s.cfg.LocalProxies[j].ServerID == id {
					s.cfg.LocalProxies[j].ServerID = ""
					s.cfg.LocalProxies[j].RemoteStatus = ""
					s.cfg.LocalProxies[j].RemoteMessage = ""
				}
			}
			return s.persist()
		}
	}
	return errors.New("代理服务器不存在")
}

func (s *ProxyService) ListNodes() ([]ClientNode, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensureLoaded(); err != nil {
		return nil, err
	}
	list := make([]ClientNode, len(s.cfg.Nodes))
	copy(list, s.cfg.Nodes)
	return list, nil
}

func (s *ProxyService) SaveNode(node ClientNode) (ClientNode, error) {
	node.Name = strings.TrimSpace(node.Name)
	node.Host = normalizeHost(node.Host)
	node.Username = strings.TrimSpace(node.Username)
	node.AuthType = strings.ToLower(strings.TrimSpace(node.AuthType))
	if node.AuthType == "" {
		node.AuthType = "password"
	}
	if node.SSHPort == 0 {
		node.SSHPort = 22
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensureLoaded(); err != nil {
		return node, err
	}

	// 编辑时如果密码/私钥留空，沿用已保存的凭据。
	if node.ID != "" {
		for _, existing := range s.cfg.Nodes {
			if existing.ID == node.ID {
				if node.Password == "" {
					node.Password = existing.Password
				}
				if node.PrivateKey == "" {
					node.PrivateKey = existing.PrivateKey
				}
				if node.OS == "" {
					node.OS = existing.OS
				}
				break
			}
		}
	}

	if node.Name == "" {
		return node, errors.New("名称不能为空")
	}
	if node.AuthType == "local" {
		node.Host = "127.0.0.1"
		node.Username = "local"
		node.SSHPort = 22
	} else {
		if node.Host == "" {
			return node, errors.New("节点地址不能为空")
		}
		if node.Username == "" {
			return node, errors.New("SSH 用户名不能为空")
		}
		if node.SSHPort < 1 || node.SSHPort > 65535 {
			return node, errors.New("SSH 端口必须在 1-65535 之间")
		}
	}
	switch node.AuthType {
	case "local":
	case "key":
		if strings.TrimSpace(node.PrivateKey) == "" {
			return node, errors.New("请填写 SSH 私钥")
		}
	case "password":
		if node.Password == "" {
			return node, errors.New("请填写 SSH 密码")
		}
	default:
		return node, errors.New("不支持的认证方式")
	}

	if node.ID == "" {
		node.ID = newID()
		s.cfg.Nodes = append(s.cfg.Nodes, node)
	} else {
		found := false
		for i := range s.cfg.Nodes {
			if s.cfg.Nodes[i].ID == node.ID {
				s.cfg.Nodes[i] = node
				found = true
				break
			}
		}
		if !found {
			return node, errors.New("要编辑的客户端节点不存在")
		}
	}

	if err := s.persist(); err != nil {
		return node, err
	}
	return node, nil
}

func (s *ProxyService) DeleteNode(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensureLoaded(); err != nil {
		return err
	}
	for i := range s.cfg.Nodes {
		if s.cfg.Nodes[i].ID == id {
			s.cfg.Nodes = append(s.cfg.Nodes[:i], s.cfg.Nodes[i+1:]...)
			for j := range s.cfg.LocalProxies {
				if s.cfg.LocalProxies[j].NodeID == id {
					s.cfg.LocalProxies[j].NodeID = ""
				}
			}
			return s.persist()
		}
	}
	return errors.New("客户端节点不存在")
}

// CheckServerPort 从本机探测公网服务器的 frps 端口是否可访问。
func (s *ProxyService) CheckServerPort(server ProxyServer) (ProxyServer, error) {
	if strings.TrimSpace(server.Host) == "" {
		return server, errors.New("公网服务器地址不能为空")
	}
	port := server.FrpsPort
	if port == 0 {
		port = 7000
	}
	if port < 1 || port > 65535 {
		return server, errors.New("frps 端口必须在 1-65535 之间")
	}

	address := net.JoinHostPort(strings.TrimSpace(server.Host), strconv.Itoa(port))
	started := time.Now()
	conn, err := net.DialTimeout("tcp", address, 5*time.Second)
	if err != nil {
		server.PortStatus = "failed"
		server.PortMessage = fmt.Sprintf("无法连接 %s: %v", address, err)
	} else {
		_ = conn.Close()
		server.PortStatus = "ok"
		server.PortMessage = fmt.Sprintf("端口可访问（%d ms）", time.Since(started).Milliseconds())
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensureLoaded(); err != nil {
		return server, err
	}
	for i := range s.cfg.Servers {
		if s.cfg.Servers[i].ID == server.ID {
			s.cfg.Servers[i] = server
			if err := s.persist(); err != nil {
				return server, err
			}
			break
		}
	}
	return server, nil
}

func normalizeHost(value string) string {
	value = strings.TrimSpace(value)
	value = strings.TrimPrefix(value, "http://")
	value = strings.TrimPrefix(value, "https://")
	return strings.TrimSuffix(value, "/")
}

func newID() string {
	return randomHex(8)
}

func newToken() string {
	return randomHex(16)
}

func randomHex(size int) string {
	buf := make([]byte, size)
	if _, err := rand.Read(buf); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 16)
	}
	return hex.EncodeToString(buf)
}
