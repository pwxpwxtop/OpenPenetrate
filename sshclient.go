package main

import (
	"bytes"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"golang.org/x/crypto/ssh"
)

func validateConnection(server ProxyServer) error {
	if strings.TrimSpace(server.Host) == "" {
		return errors.New("公网服务器地址不能为空")
	}
	if strings.TrimSpace(server.Username) == "" {
		return errors.New("SSH 用户名不能为空")
	}
	port := server.SSHPort
	if port == 0 {
		port = 22
	}
	if port < 1 || port > 65535 {
		return errors.New("SSH 端口必须在 1-65535 之间")
	}
	if server.AuthType == "key" {
		if strings.TrimSpace(server.PrivateKey) == "" {
			return errors.New("请填写 SSH 私钥")
		}
		return nil
	}
	if server.Password == "" {
		return errors.New("请填写 SSH 密码")
	}
	return nil
}

func dialSSH(server ProxyServer) (*ssh.Client, error) {
	port := server.SSHPort
	if port == 0 {
		port = 22
	}

	config := &ssh.ClientConfig{
		User: strings.TrimSpace(server.Username),
		// 首次连接即信任目标主机密钥。本工具用于管理用户自己的服务器，
		// 如需更严格的安全策略可在此改为指纹校验。
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         15 * time.Second,
	}

	if server.AuthType == "key" {
		signer, err := ssh.ParsePrivateKey([]byte(server.PrivateKey))
		if err != nil {
			return nil, fmt.Errorf("解析 SSH 私钥失败: %w", err)
		}
		config.Auth = []ssh.AuthMethod{ssh.PublicKeys(signer)}
	} else {
		config.Auth = []ssh.AuthMethod{
			ssh.Password(server.Password),
			ssh.KeyboardInteractive(func(_, _ string, questions []string, _ []bool) ([]string, error) {
				answers := make([]string, len(questions))
				for i := range answers {
					answers[i] = server.Password
				}
				return answers, nil
			}),
		}
	}

	addr := net.JoinHostPort(strings.TrimSpace(server.Host), strconv.Itoa(port))
	client, err := ssh.Dial("tcp", addr, config)
	if err != nil {
		return nil, fmt.Errorf("SSH 连接失败: %w", err)
	}
	return client, nil
}

// TestServer 连接目标服务器并返回系统信息，用于表单中的“测试连接”。
func (s *ProxyService) TestServer(server ProxyServer) (string, error) {
	if err := validateConnection(server); err != nil {
		return "", err
	}

	client, err := dialSSH(server)
	if err != nil {
		return "", err
	}
	defer client.Close()

	parts := []string{"连接成功"}

	if uid, _ := runRemote(client, "id -u"); strings.TrimSpace(uid) == "0" {
		parts = append(parts, "root 权限")
	} else {
		parts = append(parts, "普通用户")
	}
	if osName, _ := runRemote(client, `sed -n 's/^PRETTY_NAME=//p' /etc/os-release 2>/dev/null | tr -d '"'`); osName != "" {
		parts = append(parts, osName)
	}
	if arch, _ := runRemote(client, "uname -m"); arch != "" {
		parts = append(parts, arch)
	}
	versionCmd := "if [ -x /opt/frp/frps ]; then /opt/frp/frps -v; elif command -v frps >/dev/null 2>&1; then frps -v; fi"
	if version, _ := runRemote(client, versionCmd); version != "" {
		parts = append(parts, "已安装 "+version)
	} else {
		parts = append(parts, "未安装 frps")
	}

	return strings.Join(parts, " · "), nil
}

// DetectFrps 通过 SSH 检测服务器上 frps 的安装与运行状态，并更新本地记录。
func (s *ProxyService) DetectFrps(server ProxyServer) (ProxyServer, error) {
	if err := validateConnection(server); err != nil {
		return server, err
	}
	client, err := dialSSH(server)
	if err != nil {
		return server, err
	}
	defer client.Close()

	version, _ := runRemote(client, "if [ -x /opt/frp/frps ]; then /opt/frp/frps -v 2>/dev/null; elif command -v frps >/dev/null 2>&1; then frps -v 2>/dev/null; fi")
	status, _ := runRemote(client, "systemctl is-active frps 2>/dev/null || true")
	status = strings.TrimSpace(status)

	server.Installed = strings.TrimSpace(version) != "" || status == "active"
	if status == "" && server.Installed {
		status = "installed"
	}
	server.Status = status

	s.mu.Lock()
	if err := s.ensureLoaded(); err == nil {
		for i := range s.cfg.Servers {
			if s.cfg.Servers[i].ID == server.ID {
				s.cfg.Servers[i].Installed = server.Installed
				s.cfg.Servers[i].Status = server.Status
				_ = s.persist()
				break
			}
		}
	}
	s.mu.Unlock()

	return server, nil
}

func runRemote(client *ssh.Client, command string) (string, error) {
	session, err := client.NewSession()
	if err != nil {
		return "", err
	}
	defer session.Close()

	var out bytes.Buffer
	session.Stdout = &out
	session.Stderr = &out
	err = session.Run(command)
	return strings.TrimSpace(out.String()), err
}

func runStreaming(client *ssh.Client, command, stdin string, emit func(string)) error {
	session, err := client.NewSession()
	if err != nil {
		return err
	}
	defer session.Close()

	writer := &lineWriter{emit: emit}
	session.Stdout = writer
	session.Stderr = writer
	if stdin != "" {
		session.Stdin = strings.NewReader(stdin)
	}

	err = session.Run(command)
	writer.flush()
	if exitErr, ok := err.(*ssh.ExitError); ok {
		return fmt.Errorf("远程命令退出码 %d", exitErr.ExitStatus())
	}
	return err
}

// lineWriter 把远程输出按行转发给前端，避免日志一次性堆到结束时才显示。
type lineWriter struct {
	mu   sync.Mutex
	buf  bytes.Buffer
	emit func(string)
}

func (w *lineWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	w.buf.Write(p)
	for {
		data := w.buf.Bytes()
		idx := bytes.IndexByte(data, '\n')
		if idx < 0 {
			break
		}
		line := strings.TrimRight(string(data[:idx]), "\r")
		w.emit(line)
		w.buf.Next(idx + 1)
	}
	return len(p), nil
}

func (w *lineWriter) flush() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.buf.Len() == 0 {
		return
	}
	line := strings.TrimRight(w.buf.String(), "\r\n")
	if line != "" {
		w.emit(line)
	}
	w.buf.Reset()
}

func emitInstallLog(line string) {
	app := application.Get()
	if app == nil {
		return
	}
	app.Event.Emit("frps:install:log", line)
}
