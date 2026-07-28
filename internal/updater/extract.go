package updater

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// maxBinarySize 解压时的单文件上限，防御畸形/恶意压缩包（当前二进制约 15MB）。
const maxBinarySize = 200 << 20 // 200MB

// extractBinary 把下载到的 release 资产还原成可执行文件，写入 dest。
//
// 存在的理由：release 发布的是 .tar.gz / .zip，而不是裸二进制。
// 之前这里直接把压缩包本身当成新 exe 覆盖掉正在运行的程序，更新一次就再也起不来。
func extractBinary(srcPath, dest string) error {
	lower := strings.ToLower(srcPath)
	switch {
	case strings.HasSuffix(lower, ".tar.gz"), strings.HasSuffix(lower, ".tgz"):
		return extractTarGz(srcPath, dest)
	case strings.HasSuffix(lower, ".zip"):
		return extractZip(srcPath, dest)
	case strings.HasSuffix(lower, ".gz"):
		return extractGz(srcPath, dest)
	default:
		// 裸二进制：直接搬过去
		return os.Rename(srcPath, dest)
	}
}

func extractTarGz(srcPath, dest string) error {
	f, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer f.Close()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("gzip reader: %w", err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return fmt.Errorf("压缩包内没有找到可执行文件")
		}
		if err != nil {
			return fmt.Errorf("read tar: %w", err)
		}
		if hdr.Typeflag != tar.TypeReg || !looksLikeBinary(hdr.Name) {
			continue
		}
		return writeFile(dest, io.LimitReader(tr, maxBinarySize))
	}
}

func extractZip(srcPath, dest string) error {
	zr, err := zip.OpenReader(srcPath)
	if err != nil {
		return fmt.Errorf("open zip: %w", err)
	}
	defer zr.Close()

	for _, entry := range zr.File {
		if entry.FileInfo().IsDir() || !looksLikeBinary(entry.Name) {
			continue
		}
		rc, err := entry.Open()
		if err != nil {
			return fmt.Errorf("open zip entry: %w", err)
		}
		defer rc.Close()
		return writeFile(dest, io.LimitReader(rc, maxBinarySize))
	}
	return fmt.Errorf("压缩包内没有找到可执行文件")
}

func extractGz(srcPath, dest string) error {
	f, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer f.Close()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("gzip reader: %w", err)
	}
	defer gz.Close()

	return writeFile(dest, io.LimitReader(gz, maxBinarySize))
}

// looksLikeBinary 判断压缩包内某个成员是不是我们要的可执行文件。
// 只认 basename，忽略压缩包内的目录层级；跳过 __MACOSX、点文件等噪音。
func looksLikeBinary(name string) bool {
	base := path.Base(filepath.ToSlash(name))
	if base == "" || strings.HasPrefix(base, ".") || strings.Contains(name, "__MACOSX") {
		return false
	}
	lower := strings.ToLower(base)
	return lower == "sanhu" || lower == "sanhu.exe" || strings.HasPrefix(lower, "sanhu-")
}

func writeFile(dest string, r io.Reader) error {
	out, err := os.OpenFile(dest, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0755)
	if err != nil {
		return fmt.Errorf("create %s: %w", dest, err)
	}
	defer out.Close()

	n, err := io.Copy(out, r)
	if err != nil {
		return fmt.Errorf("write %s: %w", dest, err)
	}
	if n == 0 {
		return fmt.Errorf("解压出的文件是空的")
	}
	return nil
}
