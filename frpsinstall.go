package main

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

// 当 GitHub API 不可用时使用的 frp 版本。
const frpFallbackVersion = "0.71.0"

// InstallFrps 通过 SSH 在公网 Linux 服务器上安装 frps，并注册为 systemd 服务。
// 安装过程通过 "frps:install:log" 事件实时推送日志。
func (s *ProxyService) InstallFrps(server ProxyServer) (ProxyServer, error) {
	return s.installFrps(server, nil)
}

// installFrps 是安装实现；extraLog 不为空时，同时把安装日志转发到调用方（例如 frpc 启动日志）。
func (s *ProxyService) installFrps(server ProxyServer, extraLog func(string)) (ProxyServer, error) {
	emit := func(line string) {
		emitInstallLog(line)
		if extraLog != nil {
			extraLog(line)
		}
	}
	if err := validateConnection(server); err != nil {
		return server, err
	}
	if server.FrpsPort < 1 || server.FrpsPort > 65535 {
		return server, errors.New("frps 端口必须在 1-65535 之间")
	}
	if server.Token == "" {
		server.Token = newToken()
	}

	port := server.SSHPort
	if port == 0 {
		port = 22
	}
	emit(fmt.Sprintf("正在连接 %s ...", net.JoinHostPort(server.Host, strconv.Itoa(port))))
	client, err := dialSSH(server)
	if err != nil {
		emit("连接失败: " + err.Error())
		return server, err
	}
	defer client.Close()
	emit("SSH 连接成功")

	sudoPrefix := ""
	sudoStdin := ""
	if uid, _ := runRemote(client, "id -u"); strings.TrimSpace(uid) == "0" {
		emit("当前用户为 root")
	} else if _, err := runRemote(client, "sudo -n true"); err == nil {
		sudoPrefix = "sudo -n "
		emit("当前用户非 root，将使用免密 sudo")
	} else if server.AuthType != "key" && server.Password != "" {
		sudoPrefix = "sudo -S -p '' "
		sudoStdin = server.Password + "\n"
		emit("当前用户非 root，将使用 sudo（密码同 SSH 密码）")
	} else {
		err := errors.New("当前 SSH 用户不是 root，且无法免密 sudo。请使用 root 账号，或为该用户配置 NOPASSWD sudo")
		emit(err.Error())
		return server, err
	}

	// 优先使用随程序打包的 frps，避免服务器无法访问外网时安装失败。
	if installed, bundleErr := s.installFrpsBundled(client, server, sudoPrefix, sudoStdin, emit); bundleErr != nil {
		emit("内置 frps 安装不可用，改用在线安装: " + bundleErr.Error())
	} else if installed {
		return s.finalizeFrpsInstall(client, server, emit)
	}

	remotePath := "/tmp/ip_frps_" + newID() + ".sh"
	script := buildFrpsInstallScript(server)
	encoded := base64.StdEncoding.EncodeToString([]byte(script))
	uploadCmd := fmt.Sprintf("printf '%%s' '%s' | base64 -d > '%s' && chmod 700 '%s'", encoded, remotePath, remotePath)
	if out, err := runRemote(client, uploadCmd); err != nil {
		detail := strings.TrimSpace(out)
		if detail == "" {
			detail = err.Error()
		}
		emit("上传安装脚本失败: " + detail)
		return server, fmt.Errorf("上传安装脚本失败: %w", err)
	}
	emit("安装脚本已上传: " + remotePath)

	runCmd := sudoPrefix + "bash " + remotePath
	runErr := runStreaming(client, runCmd, sudoStdin, emitInstallLog)
	_, _ = runRemote(client, "rm -f '"+remotePath+"'")

	if runErr != nil {
		emit("安装失败: " + runErr.Error())
		return server, fmt.Errorf("安装 frps 失败: %w", runErr)
	}

	return s.finalizeFrpsInstall(client, server, emit)
}

// finalizeFrpsInstall 记录安装结果并同步本地服务器配置。
func (s *ProxyService) finalizeFrpsInstall(client *ssh.Client, server ProxyServer, emit func(string)) (ProxyServer, error) {
	status, _ := runRemote(client, "systemctl is-active frps 2>/dev/null")
	status = strings.TrimSpace(status)
	if status == "" {
		status = "unknown"
	}
	emit("安装完成，frps 当前状态: " + status)

	server.Installed = true
	server.Status = status

	// 如果该服务器已保存到本地配置，同步安装状态。
	s.mu.Lock()
	if err := s.ensureLoaded(); err == nil {
		for i := range s.cfg.Servers {
			if s.cfg.Servers[i].ID == server.ID {
				s.cfg.Servers[i] = server
				_ = s.persist()
				break
			}
		}
	}
	s.mu.Unlock()

	return server, nil
}

// installFrpsBundled 使用内置的 frp 包安装 frps，返回 true 表示安装成功。
func (s *ProxyService) installFrpsBundled(client *ssh.Client, server ProxyServer, sudoPrefix, sudoStdin string, emit func(string)) (bool, error) {
	arch, err := detectRemoteArch(client, "linux")
	if err != nil {
		return false, err
	}
	data, name, ok := bundledFrpPackage("linux", arch)
	if !ok {
		return false, fmt.Errorf("没有 linux/%s 的内置 frp 包", arch)
	}
	binary, err := extractTarGzBinary(data, "frps")
	if err != nil {
		return false, err
	}
	emit("发现内置 frp 包: " + name)

	remoteBin := "/tmp/ip_frps_bin_" + newID()
	if err := uploadBytes(client, binary, remoteBin); err != nil {
		return false, fmt.Errorf("上传内置 frps 失败: %w", err)
	}
	scriptPath := "/tmp/ip_frps_bundle_" + newID() + ".sh"
	if err := uploadBytes(client, []byte(buildFrpsInstallScriptFromBinary(server, remoteBin)), scriptPath); err != nil {
		_, _ = runRemote(client, "rm -f '"+remoteBin+"'")
		return false, fmt.Errorf("上传安装脚本失败: %w", err)
	}

	runErr := runStreaming(client, sudoPrefix+"bash "+scriptPath, sudoStdin, emit)
	_, _ = runRemote(client, "rm -f '"+scriptPath+"' '"+remoteBin+"'")
	if runErr != nil {
		return false, fmt.Errorf("安装 frps 失败: %w", runErr)
	}
	return true, nil
}

// startFrpsService 尝试启动已安装但未运行的 frps。
func (s *ProxyService) startFrpsService(server ProxyServer) error {
	client, err := dialSSH(server)
	if err != nil {
		return err
	}
	defer client.Close()

	prefix, stdin, err := sudoContext(client, server.AuthType, server.Password)
	if err != nil {
		return err
	}
	if out, err := runRemoteWithStdin(client, prefix+"systemctl restart frps", stdin); err != nil {
		return fmt.Errorf("启动 frps 失败: %s", strings.TrimSpace(out))
	}
	time.Sleep(2 * time.Second)
	status, _ := runRemote(client, "systemctl is-active frps 2>/dev/null")
	if strings.TrimSpace(status) != "active" {
		logs, _ := runRemote(client, "journalctl -u frps -n 20 --no-pager 2>/dev/null")
		return fmt.Errorf("frps 未能启动: %s", strings.TrimSpace(logs))
	}
	return nil
}

// buildFrpsInstallScriptFromBinary 生成使用已上传二进制安装 frps 的脚本。
func buildFrpsInstallScriptFromBinary(server ProxyServer, remoteBin string) string {
	frpsConfig := fmt.Sprintf("bindPort = %d\n\nauth.method = \"token\"\nauth.token = %q\n", server.FrpsPort, server.Token)
	configB64 := base64.StdEncoding.EncodeToString([]byte(frpsConfig))

	return fmt.Sprintf(`#!/usr/bin/env bash
set -euo pipefail

echo "==> 使用内置 frp 包安装 frps"
BIN='%s'
# 通过 SSH 上传的文件默认没有执行权限，先补上再验证。
chmod 755 "$BIN" 2>/dev/null || true
if ! "$BIN" -v >/tmp/ip_frps_version.log 2>&1; then
  echo "错误: 内置 frps 无法在当前系统运行:"
  cat /tmp/ip_frps_version.log 2>/dev/null || true
  rm -f /tmp/ip_frps_version.log
  exit 1
fi
rm -f /tmp/ip_frps_version.log

mkdir -p /opt/frp /etc/frp
install -m 0755 "$BIN" /opt/frp/frps
echo "    frps 已安装到 /opt/frp/frps"

printf '%%s' '%s' | base64 -d > /etc/frp/frps.toml
chmod 600 /etc/frp/frps.toml
echo "    bindPort = %d"

if ! command -v systemctl >/dev/null 2>&1; then
  echo "错误: 未检测到 systemd，无法自动注册服务"
  exit 1
fi
cat > /etc/systemd/system/frps.service <<'UNIT_EOF'
[Unit]
Description=frp server (frps)
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
Restart=always
RestartSec=5s
User=root
ExecStart=/opt/frp/frps -c /etc/frp/frps.toml

[Install]
WantedBy=multi-user.target
UNIT_EOF
systemctl daemon-reload
systemctl enable frps >/dev/null 2>&1 || true
systemctl restart frps
sleep 2
if systemctl is-active --quiet frps; then
  echo "    frps 已启动，监听端口 %d/tcp"
  /opt/frp/frps -v || true
  echo "提示: 请在云服务器安全组/防火墙中放行 %d/tcp"
else
  echo "    frps 启动失败，最近日志:"
  journalctl -u frps -n 30 --no-pager || true
  exit 1
fi
`, remoteBin, configB64, server.FrpsPort, server.FrpsPort, server.FrpsPort)
}

func buildFrpsInstallScript(server ProxyServer) string {
	frpsConfig := fmt.Sprintf("bindPort = %d\n\nauth.method = \"token\"\nauth.token = %q\n", server.FrpsPort, server.Token)
	configB64 := base64.StdEncoding.EncodeToString([]byte(frpsConfig))

	script := `#!/usr/bin/env bash
set -euo pipefail

echo "==> frps 安装脚本 v3（镜像回退 + i686 兼容）"

echo "==> [1/6] 检测系统与 CPU 架构"
ARCH="$(uname -m)"
case "$ARCH" in
  x86_64|amd64) FRP_ARCH="amd64" ;;
  aarch64|arm64) FRP_ARCH="arm64" ;;
  armv7l|armv7|armhf) FRP_ARCH="arm" ;;
  i386|i686)
    # frp 官方已停止发布 linux/386 构建。多数 i686 系统实际运行在 64 位内核上，
    # 静态编译的 amd64 版本可以直接执行，这里优先尝试。
    FRP_ARCH="amd64"
    echo "    系统报告 ${ARCH}，但 frp 已无 32 位官方构建，尝试使用 linux/amd64 静态版本"
    ;;
  *) echo "不支持的 CPU 架构: $ARCH"; exit 1 ;;
esac
echo "    系统架构: $ARCH -> $FRP_ARCH"

if command -v curl >/dev/null 2>&1; then
  FETCH="curl"
elif command -v wget >/dev/null 2>&1; then
  FETCH="wget"
else
  echo "错误: 系统缺少 curl 或 wget，无法下载 frp"
  exit 1
fi

echo "==> [2/6] 获取 frp 最新版本"
VERSION=""
if [ "$FETCH" = "curl" ]; then
  VERSION="$(curl -fsSL --connect-timeout 10 https://api.github.com/repos/fatedier/frp/releases/latest 2>/dev/null | sed -n 's/.*"tag_name":[[:space:]]*"v\([^"]*\)".*/\1/p' | head -n1 || true)"
else
  VERSION="$(wget -qO- --timeout=10 https://api.github.com/repos/fatedier/frp/releases/latest 2>/dev/null | sed -n 's/.*"tag_name":[[:space:]]*"v\([^"]*\)".*/\1/p' | head -n1 || true)"
fi
if [ -z "$VERSION" ]; then
  VERSION="__DEFAULT_VERSION__"
  echo "    未获取到最新版本，使用默认版本 v$VERSION"
else
  echo "    最新版本: v$VERSION"
fi

echo "==> [3/6] 下载并安装 frp"
TMP_DIR="$(mktemp -d)"
trap 'rm -rf "$TMP_DIR"' EXIT
PKG="frp_${VERSION}_linux_${FRP_ARCH}.tar.gz"
ORIGIN="https://github.com/fatedier/frp/releases/download/v${VERSION}/${PKG}"
URLS="$ORIGIN
https://ghfast.top/$ORIGIN
https://gh-proxy.com/$ORIGIN
https://ghproxy.net/$ORIGIN
https://github.moeyy.xyz/$ORIGIN"

DOWNLOADED=0
for URL in $URLS; do
  echo "    尝试下载: $URL"
  if [ "$FETCH" = "curl" ]; then
    # 官方地址保持完整证书校验；第三方加速镜像在旧系统上常因 CA 证书过期失败，使用兼容模式。
    case "$URL" in
      https://github.com/*) CURL_FLAGS="-fsSL" ;;
      *) CURL_FLAGS="-fsSLk" ;;
    esac
    if curl $CURL_FLAGS --connect-timeout 10 --max-time 600 --speed-time 30 --speed-limit 1024 -o "$TMP_DIR/$PKG" "$URL"; then
      :
    fi
  else
    case "$URL" in
      https://github.com/*) WGET_FLAGS="-q" ;;
      *) WGET_FLAGS="-q --no-check-certificate" ;;
    esac
    if wget $WGET_FLAGS --timeout=20 --tries=1 -O "$TMP_DIR/$PKG" "$URL"; then
      :
    fi
  fi
  if [ -s "$TMP_DIR/$PKG" ] && tar -tzf "$TMP_DIR/$PKG" >/dev/null 2>&1; then
    DOWNLOADED=1
    echo "    下载成功"
    break
  fi
  rm -f "$TMP_DIR/$PKG"
  echo "    该下载源不可用，尝试下一个"
done

if [ "$DOWNLOADED" -ne 1 ]; then
  echo "错误: 所有下载源均失败，请检查服务器网络后重试"
  exit 1
fi

tar -xzf "$TMP_DIR/$PKG" -C "$TMP_DIR"
FRPS_BIN="$TMP_DIR/frp_${VERSION}_linux_${FRP_ARCH}/frps"
if ! "$FRPS_BIN" -v >/dev/null 2>&1; then
  echo "错误: 当前系统无法运行 linux/${FRP_ARCH} 版本的 frps"
  echo "      系统架构为 ${ARCH}。frp 官方已不再提供 32 位 Linux 构建，"
  echo "      请更换 64 位系统，或自行交叉编译 linux/386 版本的 frps。"
  exit 1
fi
mkdir -p /opt/frp
install -m 0755 "$FRPS_BIN" /opt/frp/frps
echo "    frps 已安装到 /opt/frp/frps"

echo "==> [4/6] 写入配置 /etc/frp/frps.toml"
mkdir -p /etc/frp
printf '%s' '__FRPS_CONFIG_B64__' | base64 -d > /etc/frp/frps.toml
chmod 600 /etc/frp/frps.toml
echo "    bindPort = __BIND_PORT__"

echo "==> [5/6] 注册 systemd 服务"
if ! command -v systemctl >/dev/null 2>&1; then
  echo "错误: 未检测到 systemd，无法自动注册服务"
  exit 1
fi
cat > /etc/systemd/system/frps.service <<'UNIT_EOF'
[Unit]
Description=frp server (frps)
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
Restart=always
RestartSec=5s
User=root
ExecStart=/opt/frp/frps -c /etc/frp/frps.toml

[Install]
WantedBy=multi-user.target
UNIT_EOF
systemctl daemon-reload
systemctl enable frps >/dev/null 2>&1 || true

echo "==> [6/6] 启动 frps"
systemctl restart frps
sleep 2
if systemctl is-active --quiet frps; then
  echo "    frps 已启动，监听端口 __BIND_PORT__/tcp"
  /opt/frp/frps -v || true
  echo "提示: 请在云服务器安全组/防火墙中放行 __BIND_PORT__/tcp"
else
  echo "    frps 启动失败，最近日志:"
  journalctl -u frps -n 30 --no-pager || true
  exit 1
fi
`

	return strings.NewReplacer(
		"__FRPS_CONFIG_B64__", configB64,
		"__BIND_PORT__", strconv.Itoa(server.FrpsPort),
		"__DEFAULT_VERSION__", frpFallbackVersion,
	).Replace(script)
}
