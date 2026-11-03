package main

import (
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
	"unicode/utf16"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

func osLabel(osName string) string {
	switch osName {
	case "windows":
		return "Windows"
	case "darwin":
		return "macOS"
	case "linux":
		return "Linux"
	default:
		return osName
	}
}

func detectRemoteOS(client *ssh.Client) (string, error) {
	unameOut, _ := runRemote(client, "uname -s 2>/dev/null")
	value := strings.ToLower(strings.TrimSpace(unameOut))
	switch {
	case strings.Contains(value, "linux"):
		return "linux", nil
	case strings.Contains(value, "darwin"):
		return "darwin", nil
	case strings.Contains(value, "mingw"), strings.Contains(value, "msys"), strings.Contains(value, "cygwin"):
		return "windows", nil
	}

	if out, _ := runRemote(client, "cmd /c ver 2>nul"); strings.Contains(strings.ToLower(out), "windows") {
		return "windows", nil
	}
	if out, _ := runRemote(client, "cmd /c echo %OS% 2>nul"); strings.Contains(strings.ToLower(out), "windows_nt") {
		return "windows", nil
	}
	if out, _ := runPowerShell(client, "[System.Environment]::OSVersion.Platform.ToString()"); strings.Contains(strings.ToLower(out), "win") {
		return "windows", nil
	}
	if out, _ := runRemote(client, "test -f /etc/os-release && echo linux || true"); strings.Contains(strings.ToLower(out), "linux") {
		return "linux", nil
	}
	if out, _ := runRemote(client, "test -d /Users && echo mac || true"); strings.Contains(strings.ToLower(out), "mac") {
		return "darwin", nil
	}
	return "", fmt.Errorf("无法识别远程操作系统（uname 输出: %q）", strings.TrimSpace(unameOut))
}

func detectRemoteArch(client *ssh.Client, osName string) (string, error) {
	if osName == "windows" {
		out, err := runPowerShell(client, "$env:PROCESSOR_ARCHITECTURE")
		if err == nil {
			if arch := normalizeArch(out); arch != "" {
				return arch, nil
			}
		}
		if out, _ := runRemote(client, "wmic os get osarchitecture /value 2>nul"); strings.Contains(strings.ToLower(out), "64-bit") {
			return "amd64", nil
		}
		return "", fmt.Errorf("无法识别 Windows CPU 架构（PowerShell 输出: %q）", strings.TrimSpace(out))
	}

	for _, command := range []string{"uname -m", "arch", "uname -p"} {
		out, err := runRemote(client, command)
		if err != nil {
			continue
		}
		if arch := normalizeArch(out); arch != "" {
			return arch, nil
		}
	}
	if out, _ := runRemote(client, "getconf LONG_BIT 2>/dev/null"); strings.TrimSpace(out) == "64" {
		return "amd64", nil
	}
	return "", errors.New("无法识别远程 CPU 架构，请在目标机器执行 uname -m 检查")
}

func normalizeArch(output string) string {
	value := strings.ToLower(strings.TrimSpace(output))
	switch {
	case strings.Contains(value, "aarch64"), strings.Contains(value, "arm64"):
		return "arm64"
	case strings.Contains(value, "x86_64"), strings.Contains(value, "amd64"), strings.Contains(value, "x64"):
		return "amd64"
	case strings.Contains(value, "armv7"), strings.Contains(value, "armhf"):
		return "arm"
	case strings.Contains(value, "i386"), strings.Contains(value, "i686"):
		return "amd64"
	}
	return ""
}

func frpPlatformFor(osName, arch string) (frpPlatformInfo, bool) {
	switch osName {
	case "linux", "darwin":
		if arch == "amd64" || arch == "arm64" || arch == "arm" {
			return frpPlatformInfo{os: osName, arch: arch}, true
		}
	case "windows":
		if arch == "amd64" || arch == "arm64" {
			return frpPlatformInfo{os: osName, arch: arch}, true
		}
	}
	return frpPlatformInfo{}, false
}

func runPowerShell(client *ssh.Client, script string) (string, error) {
	encoded := powershellEncoded(script)
	return runRemote(client, "powershell -NoProfile -ExecutionPolicy Bypass -EncodedCommand "+encoded)
}

func powershellEncoded(script string) string {
	encoded := utf16.Encode([]rune(script))
	buf := make([]byte, len(encoded)*2)
	for i, r := range encoded {
		binary.LittleEndian.PutUint16(buf[i*2:], r)
	}
	return base64.StdEncoding.EncodeToString(buf)
}

func uploadFileSFTP(client *ssh.Client, data []byte, remotePath string) error {
	sftpClient, err := sftp.NewClient(client)
	if err != nil {
		return err
	}
	defer sftpClient.Close()

	file, err := sftpClient.Create(remotePath)
	if err != nil {
		return err
	}
	defer file.Close()
	_, err = file.Write(data)
	return err
}

func parseKeyValues(output string) map[string]string {
	values := make(map[string]string)
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if idx := strings.Index(line, "="); idx > 0 {
			values[strings.TrimSpace(line[:idx])] = strings.TrimSpace(line[idx+1:])
		}
	}
	return values
}
