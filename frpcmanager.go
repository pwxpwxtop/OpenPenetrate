package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
)

const frpcMaxLogLines = 500

// FrpcStatus 描述某个本地代理对应的 frpc 进程状态。
type FrpcStatus struct {
	ProxyID string `json:"proxyId"`
	Running bool   `json:"running"`
	Message string `json:"message"`
	PID     int    `json:"pid"`
}

// FrpcLogEvent 是 frpc 实时日志事件的数据。
type FrpcLogEvent struct {
	ProxyID string `json:"proxyId"`
	Line    string `json:"line"`
}

type frpcProcess struct {
	cmd        *exec.Cmd
	configPath string
	logs       []string
	running    bool
	exited     chan struct{}
}

// StartFrpc 为指定本地代理生成 frpc 配置并启动本机 frpc 进程。
func (s *ProxyService) StartFrpc(proxyID string) (FrpcStatus, error) {
	s.mu.Lock()
	if err := s.ensureLoaded(); err != nil {
		s.mu.Unlock()
		return FrpcStatus{ProxyID: proxyID}, err
	}

	var proxy LocalProxy
	proxyFound := false
	for _, item := range s.cfg.LocalProxies {
		if item.ID == proxyID {
			proxy = item
			proxyFound = true
			break
		}
	}
	var server ProxyServer
	serverFound := false
	if proxyFound && proxy.ServerID != "" {
		for _, item := range s.cfg.Servers {
			if item.ID == proxy.ServerID {
				server = item
				serverFound = true
				break
			}
		}
	}
	configDir := filepath.Join(s.dir, "frpc")
	s.mu.Unlock()

	if !proxyFound {
		return FrpcStatus{ProxyID: proxyID}, errors.New("本地代理不存在")
	}
	if proxy.ServerID == "" {
		return FrpcStatus{ProxyID: proxyID}, errors.New("请先编辑该代理并选择关联的代理服务器")
	}
	if !serverFound {
		return FrpcStatus{ProxyID: proxyID}, errors.New("关联的代理服务器不存在，请重新选择")
	}
	if proxy.RemotePort < 1 || proxy.RemotePort > 65535 {
		return FrpcStatus{ProxyID: proxyID}, errors.New("远程访问端口必须在 1-65535 之间")
	}
	if proxy.NodeID != "" {
		return FrpcStatus{ProxyID: proxyID}, errors.New("该代理已由客户端节点托管，请在「服务器挂载」页面启动对应节点")
	}

	s.frpcMu.Lock()
	proc := s.frpc[proxyID]
	if proc != nil && proc.running {
		status := frpcStatusFromProc(proxyID, proc)
		s.frpcMu.Unlock()
		return status, nil
	}
	if proc == nil {
		proc = &frpcProcess{}
		s.frpc[proxyID] = proc
	}
	proc.logs = nil
	proc.running = false
	s.frpcMu.Unlock()

	s.appendFrpcLog(proxyID, "正在准备 frpc ...")

	// 启动前从服务器同步 frps 的端口与 Token，并检查 frps 是否已安装/运行。
	discovered, discoverErr := s.DiscoverFrps(server)
	if discoverErr == nil {
		changed := false
		if discovered.BindPort > 0 && discovered.BindPort != server.FrpsPort {
			server.FrpsPort = discovered.BindPort
			s.appendFrpcLog(proxyID, fmt.Sprintf("已从服务器同步 frps 端口: %d", discovered.BindPort))
			changed = true
		}
		if discovered.Token != "" && discovered.Token != server.Token {
			server.Token = discovered.Token
			s.appendFrpcLog(proxyID, "已从服务器同步 frps Token")
			changed = true
		}
		if changed {
			s.mu.Lock()
			if loadErr := s.ensureLoaded(); loadErr == nil {
				for i := range s.cfg.Servers {
					if s.cfg.Servers[i].ID == server.ID {
						s.cfg.Servers[i].FrpsPort = server.FrpsPort
						s.cfg.Servers[i].Token = server.Token
						_ = s.persist()
						break
					}
				}
			}
			s.mu.Unlock()
		}
	}

	frpsAddress := net.JoinHostPort(server.Host, strconv.Itoa(server.FrpsPort))
	if discoverErr == nil && !discovered.Installed {
		// 服务器还没有 frps，先用内置安装包自动安装，再继续启动 frpc。
		s.appendFrpcLog(proxyID, "服务器上未检测到 frps，正在自动安装内置版本 ...")
		installed, installErr := s.installFrps(server, func(line string) {
			s.appendFrpcLog(proxyID, line)
		})
		if installErr != nil {
			s.appendFrpcLog(proxyID, "自动安装 frps 失败: "+installErr.Error())
			return FrpcStatus{ProxyID: proxyID, Message: "启动失败"}, fmt.Errorf("服务器未安装 frps，自动安装失败: %w", installErr)
		}
		server = installed
		s.appendFrpcLog(proxyID, "frps 安装完成，继续启动 frpc")
	} else if discoverErr == nil && discovered.Status != "active" && !tcpReachable(frpsAddress) {
		s.appendFrpcLog(proxyID, "检测到 frps 未运行，正在启动服务 ...")
		if err := s.startFrpsService(server); err != nil {
			s.appendFrpcLog(proxyID, "启动 frps 失败: "+err.Error())
			return FrpcStatus{ProxyID: proxyID, Message: "启动失败"}, err
		}
		s.appendFrpcLog(proxyID, "frps 已启动")
	}

	// 每次连接前刷新服务器上的缓存配置，便于其他客户端发现。
	go s.syncProxyConfigToServer(server, proxy)

	binPath, err := s.ensureFrpcBinary(func(line string) {
		s.appendFrpcLog(proxyID, line)
	})
	if err != nil {
		s.appendFrpcLog(proxyID, "frpc 准备失败: "+err.Error())
		return FrpcStatus{ProxyID: proxyID, Message: "启动失败"}, err
	}

	if err := os.MkdirAll(configDir, 0o755); err != nil {
		return FrpcStatus{ProxyID: proxyID, Message: "启动失败"}, err
	}
	configPath := filepath.Join(configDir, proxyID+".toml")
	if err := os.WriteFile(configPath, []byte(buildFrpcConfig(server, proxy)), 0o600); err != nil {
		return FrpcStatus{ProxyID: proxyID, Message: "启动失败"}, fmt.Errorf("写入 frpc 配置失败: %w", err)
	}

	writer := &lineWriter{emit: func(line string) {
		s.appendFrpcLog(proxyID, line)
		if strings.Contains(line, "token in login doesn't match") {
			s.appendFrpcLog(proxyID, "提示: 本地 Token 与服务器 frps 配置不一致。请在「代理服务器」中编辑该服务器并点击「配置发现」同步 Token 后重试")
		}
	}}

	cmd := exec.Command(binPath, "-c", configPath)
	hideCommandWindow(cmd)
	cmd.Stdout = writer
	cmd.Stderr = writer

	s.appendFrpcLog(proxyID, fmt.Sprintf("启动 frpc，连接 %s:%d", server.Host, server.FrpsPort))
	if err := cmd.Start(); err != nil {
		s.appendFrpcLog(proxyID, "启动失败: "+err.Error())
		return FrpcStatus{ProxyID: proxyID, Message: "启动失败"}, fmt.Errorf("启动 frpc 失败: %w", err)
	}

	exited := make(chan struct{})
	s.frpcMu.Lock()
	proc.cmd = cmd
	proc.configPath = configPath
	proc.running = true
	proc.exited = exited
	s.frpcMu.Unlock()

	go func() {
		waitErr := cmd.Wait()
		writer.flush()

		s.frpcMu.Lock()
		if current, ok := s.frpc[proxyID]; ok && current.cmd == cmd {
			current.running = false
		}
		close(exited)
		s.frpcMu.Unlock()

		if waitErr != nil {
			s.appendFrpcLog(proxyID, "frpc 已退出: "+waitErr.Error())
		} else {
			s.appendFrpcLog(proxyID, "frpc 已退出")
		}
	}()

	return FrpcStatus{ProxyID: proxyID, Running: true, Message: "运行中", PID: cmd.Process.Pid}, nil
}

// StopFrpc 停止指定本地代理对应的 frpc 进程。
func (s *ProxyService) StopFrpc(proxyID string) (FrpcStatus, error) {
	s.frpcMu.Lock()
	proc := s.frpc[proxyID]
	if proc == nil || !proc.running || proc.cmd == nil || proc.cmd.Process == nil {
		s.frpcMu.Unlock()
		return FrpcStatus{ProxyID: proxyID, Message: "未运行"}, nil
	}
	cmd := proc.cmd
	exited := proc.exited
	s.frpcMu.Unlock()

	if err := cmd.Process.Kill(); err != nil {
		return FrpcStatus{ProxyID: proxyID, Running: true, Message: "停止失败"}, err
	}
	if exited != nil {
		select {
		case <-exited:
		case <-time.After(5 * time.Second):
		}
	}
	return FrpcStatus{ProxyID: proxyID, Message: "已停止"}, nil
}

// GetFrpcStatus 返回指定本地代理的 frpc 运行状态。
func (s *ProxyService) GetFrpcStatus(proxyID string) FrpcStatus {
	s.frpcMu.Lock()
	defer s.frpcMu.Unlock()
	proc := s.frpc[proxyID]
	if proc == nil {
		return FrpcStatus{ProxyID: proxyID, Message: "未运行"}
	}
	return frpcStatusFromProc(proxyID, proc)
}

// ListFrpcStatuses 返回所有已管理过的 frpc 进程状态。
func (s *ProxyService) ListFrpcStatuses() []FrpcStatus {
	s.frpcMu.Lock()
	defer s.frpcMu.Unlock()
	list := make([]FrpcStatus, 0, len(s.frpc))
	for id, proc := range s.frpc {
		list = append(list, frpcStatusFromProc(id, proc))
	}
	return list
}

// GetFrpcLogs 返回指定本地代理最近的 frpc 日志。
func (s *ProxyService) GetFrpcLogs(proxyID string) []string {
	s.frpcMu.Lock()
	defer s.frpcMu.Unlock()
	proc := s.frpc[proxyID]
	if proc == nil {
		return []string{}
	}
	logs := make([]string, len(proc.logs))
	copy(logs, proc.logs)
	return logs
}

// ServiceShutdown 在应用退出时关闭所有由本应用启动的 frpc 进程。
func (s *ProxyService) ServiceShutdown() error {
	s.frpcMu.Lock()
	cmds := make([]*exec.Cmd, 0, len(s.frpc))
	for _, proc := range s.frpc {
		if proc.running && proc.cmd != nil && proc.cmd.Process != nil {
			cmds = append(cmds, proc.cmd)
		}
	}
	s.frpcMu.Unlock()

	for _, cmd := range cmds {
		_ = cmd.Process.Kill()
	}
	return nil
}

func frpcStatusFromProc(proxyID string, proc *frpcProcess) FrpcStatus {
	status := FrpcStatus{ProxyID: proxyID, Running: proc.running, Message: "未运行"}
	if proc.running {
		status.Message = "运行中"
		if proc.cmd != nil && proc.cmd.Process != nil {
			status.PID = proc.cmd.Process.Pid
		}
	}
	return status
}

func (s *ProxyService) appendFrpcLog(proxyID, line string) {
	s.frpcMu.Lock()
	if proc := s.frpc[proxyID]; proc != nil {
		proc.logs = append(proc.logs, line)
		if len(proc.logs) > frpcMaxLogLines {
			proc.logs = proc.logs[len(proc.logs)-frpcMaxLogLines:]
		}
	}
	s.frpcMu.Unlock()
	emitFrpcLog(proxyID, line)
}

func emitFrpcLog(proxyID, line string) {
	app := application.Get()
	if app == nil {
		return
	}
	app.Event.Emit("frpc:log", FrpcLogEvent{ProxyID: proxyID, Line: line})
}

func buildFrpcConfig(server ProxyServer, proxy LocalProxy) string {
	localIP := strings.TrimSpace(proxy.Host)
	if localIP == "" {
		localIP = "127.0.0.1"
	}
	return fmt.Sprintf(`serverAddr = %q
serverPort = %d

auth.method = "token"
auth.token = %q

log.to = "console"
log.level = "info"

[[proxies]]
name = %q
type = "tcp"
localIP = %q
localPort = %d
remotePort = %d
`, server.Host, server.FrpsPort, server.Token, "proxy-"+proxy.ID, localIP, proxy.Port, proxy.RemotePort)
}

func (s *ProxyService) ensureFrpcBinary(logf func(string)) (string, error) {
	exeName := "frpc"
	if runtime.GOOS == "windows" {
		exeName = "frpc.exe"
	}
	binDir := filepath.Join(s.dir, "frpc")
	binPath := filepath.Join(binDir, exeName)

	// 复用已下载的 frpc：优先本机穿透目录，其次是后台挂载节点目录。
	// 网络不可用时不再重复下载。
	candidates := []string{binPath, filepath.Join(localNodeDir(s), exeName)}
	for _, candidate := range candidates {
		info, err := os.Stat(candidate)
		if err != nil || info.Size() == 0 {
			continue
		}
		if candidate == binPath {
			return binPath, nil
		}
		logf("发现已下载的 frpc: " + candidate)
		if err := os.MkdirAll(binDir, 0o755); err == nil {
			if err := copyExecutable(candidate, binPath); err == nil {
				logf("已复用该 frpc，无需重新下载")
				return binPath, nil
			}
		}
		return candidate, nil
	}
	if path, err := exec.LookPath(exeName); err == nil {
		logf("使用系统中已安装的 frpc: " + path)
		return path, nil
	}

	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return "", err
	}

	platform, ok := frpPlatform()
	if !ok {
		return "", fmt.Errorf("当前系统 %s/%s 没有可用的 frpc 官方构建", runtime.GOOS, runtime.GOARCH)
	}
	logf("本机未找到 frpc，尝试使用内置版本")
	binary, err := s.downloadFrpcBinary(platform, logf)
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(binPath, binary, 0o755); err != nil {
		return "", err
	}
	logf("frpc 已就绪")
	return binPath, nil
}

func copyExecutable(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func (s *ProxyService) downloadFrpcBinary(platform frpPlatformInfo, logf func(string)) ([]byte, error) {
	// 优先使用随程序打包的 frpc，完全离线可用。
	if data, name, ok := bundledFrpPackage(platform.os, platform.arch); ok {
		binary, err := extractFrpcBinary(data, platform.os)
		if err == nil {
			logf("已使用内置 frpc 包: " + name)
			return binary, nil
		}
		logf("内置 frpc 包解析失败，改为在线下载: " + err.Error())
	}

	version := latestFrpVersion()
	if version == "" {
		version = frpFallbackVersion
	}
	ext := ".tar.gz"
	if platform.os == "windows" {
		ext = ".zip"
	}
	pkg := fmt.Sprintf("frp_%s_%s_%s%s", version, platform.os, platform.arch, ext)

	logf(fmt.Sprintf("正在下载 frpc v%s (%s/%s) ...", version, platform.os, platform.arch))
	data, err := s.downloadFrpAsset(logf, version, pkg)
	if err != nil {
		return nil, err
	}
	binary, err := extractFrpcBinary(data, platform.os)
	if err != nil {
		return nil, err
	}
	logf("frpc 下载完成")
	return binary, nil
}

type frpPlatformInfo struct {
	os   string
	arch string
}

func frpPlatform() (frpPlatformInfo, bool) {
	arch := runtime.GOARCH
	switch runtime.GOOS {
	case "windows", "darwin":
		if arch == "amd64" || arch == "arm64" {
			return frpPlatformInfo{os: runtime.GOOS, arch: arch}, true
		}
	case "linux":
		switch arch {
		case "amd64", "arm64", "arm", "386", "mips", "mipsle", "mips64", "mips64le", "riscv64", "loong64":
			return frpPlatformInfo{os: runtime.GOOS, arch: arch}, true
		}
	}
	return frpPlatformInfo{}, false
}

func latestFrpVersion() string {
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get("https://api.github.com/repos/fatedier/frp/releases/latest")
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ""
	}
	var payload struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return ""
	}
	return strings.TrimPrefix(strings.TrimSpace(payload.TagName), "v")
}

func (s *ProxyService) downloadFrpAsset(logf func(string), version, pkg string) ([]byte, error) {
	origin := fmt.Sprintf("https://github.com/fatedier/frp/releases/download/v%s/%s", version, pkg)
	urls := []string{
		"https://ghfast.top/" + origin,
		"https://gh-proxy.com/" + origin,
		"https://ghproxy.net/" + origin,
		"https://github.moeyy.xyz/" + origin,
		origin,
	}

	transport := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 20 * time.Second,
	}
	client := &http.Client{Transport: transport, Timeout: 2 * time.Minute}
	var lastErr error
	for _, url := range urls {
		logf("尝试下载: " + url)
		ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
		req, reqErr := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if reqErr != nil {
			cancel()
			lastErr = reqErr
			continue
		}
		resp, err := client.Do(req)
		if err != nil {
			cancel()
			lastErr = err
			logf("下载失败: " + err.Error())
			continue
		}
		if resp.StatusCode != http.StatusOK {
			lastErr = fmt.Errorf("HTTP %d", resp.StatusCode)
			resp.Body.Close()
			cancel()
			logf(fmt.Sprintf("下载失败: HTTP %d", resp.StatusCode))
			continue
		}
		data, readErr := io.ReadAll(io.LimitReader(resp.Body, 128<<20))
		resp.Body.Close()
		cancel()
		if readErr != nil {
			lastErr = readErr
			logf("下载失败: " + readErr.Error())
			continue
		}
		if len(data) > 0 {
			return data, nil
		}
		lastErr = errors.New("下载内容为空")
	}
	if lastErr == nil {
		lastErr = errors.New("没有可用的下载源")
	}
	return nil, fmt.Errorf("下载 frpc 失败: %w（请检查网络；也可手动将 frpc 放到 %s 后重试）", lastErr, filepath.Join(s.dir, "frpc"))
}

func extractFrpcBinary(data []byte, goos string) ([]byte, error) {
	if goos == "windows" {
		reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			return nil, fmt.Errorf("解析 frpc 压缩包失败: %w", err)
		}
		for _, file := range reader.File {
			if path.Base(file.Name) != "frpc.exe" {
				continue
			}
			rc, err := file.Open()
			if err != nil {
				return nil, err
			}
			defer rc.Close()
			return io.ReadAll(rc)
		}
		return nil, errors.New("压缩包中未找到 frpc.exe")
	}

	gzipReader, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("解析 frpc 压缩包失败: %w", err)
	}
	defer gzipReader.Close()

	tarReader := tar.NewReader(gzipReader)
	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if header.Typeflag == tar.TypeReg && path.Base(header.Name) == "frpc" {
			return io.ReadAll(tarReader)
		}
	}
	return nil, errors.New("压缩包中未找到 frpc")
}
