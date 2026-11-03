package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"embed"
	"errors"
	"io"
	"io/fs"
	"path"
	"sort"
)

// 随程序打包的 frp 压缩包，避免首次启动时因网络问题无法下载 frpc。
//
//go:embed assets/frp/*.zip assets/frp/*.tar.gz
var embeddedFrpAssets embed.FS

// bundledFrpPackage 查找与目标系统匹配的内置 frp 包。
func bundledFrpPackage(osName, arch string) ([]byte, string, bool) {
	ext := ".tar.gz"
	if osName == "windows" {
		ext = ".zip"
	}
	matches, err := fs.Glob(embeddedFrpAssets, "assets/frp/frp_*_"+osName+"_"+arch+ext)
	if err != nil || len(matches) == 0 {
		return nil, "", false
	}
	// 存在多个版本时优先使用版本号较高的包。
	sort.Sort(sort.Reverse(sort.StringSlice(matches)))
	data, err := embeddedFrpAssets.ReadFile(matches[0])
	if err != nil {
		return nil, "", false
	}
	return data, matches[0][len("assets/frp/"):], true
}

// extractTarGzBinary 从内置 tar.gz 包中提取指定名称的二进制。
func extractTarGzBinary(data []byte, name string) ([]byte, error) {
	gzipReader, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer gzipReader.Close()

	tarReader := tar.NewReader(gzipReader)
	for {
		header, err := tarReader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		if header.Typeflag == tar.TypeReg && path.Base(header.Name) == name {
			return io.ReadAll(tarReader)
		}
	}
	return nil, errors.New("压缩包中未找到 " + name)
}
