package updater

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"
)

// fakeELF 造一个够大、魔数正确的假二进制，让 verifyExecutable 认得
func fakeELF(size int) []byte {
	b := make([]byte, size)
	copy(b, []byte{0x7f, 'E', 'L', 'F'})
	return b
}

func writeTarGz(t *testing.T, dir, name string, entries map[string][]byte) string {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for n, content := range entries {
		if err := tw.WriteHeader(&tar.Header{Name: n, Mode: 0755, Size: int64(len(content)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatalf("tar header: %v", err)
		}
		if _, err := tw.Write(content); err != nil {
			t.Fatalf("tar write: %v", err)
		}
	}
	tw.Close()
	gz.Close()

	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, buf.Bytes(), 0644); err != nil {
		t.Fatalf("write archive: %v", err)
	}
	return p
}

func writeZip(t *testing.T, dir, name string, entries map[string][]byte) string {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for n, content := range entries {
		w, err := zw.Create(n)
		if err != nil {
			t.Fatalf("zip create: %v", err)
		}
		if _, err := w.Write(content); err != nil {
			t.Fatalf("zip write: %v", err)
		}
	}
	zw.Close()

	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, buf.Bytes(), 0644); err != nil {
		t.Fatalf("write archive: %v", err)
	}
	return p
}

// release 发的是 tar.gz，必须解压出里面的二进制，而不是把压缩包本身当 exe
func TestExtractBinaryFromTarGz(t *testing.T) {
	dir := t.TempDir()
	want := fakeELF(2 << 20)
	src := writeTarGz(t, dir, "sanhu-linux-amd64.tar.gz", map[string][]byte{
		"sanhu-linux-amd64": want,
	})

	dest := filepath.Join(dir, "sanhu.new")
	if err := extractBinary(src, dest); err != nil {
		t.Fatalf("extractBinary: %v", err)
	}
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("read dest: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("解压结果与原二进制不一致（%d vs %d 字节）", len(got), len(want))
	}
	if err := verifyExecutable(dest); err != nil {
		t.Fatalf("verifyExecutable: %v", err)
	}
}

func TestExtractBinaryFromZip(t *testing.T) {
	dir := t.TempDir()
	want := fakeELF(2 << 20)
	src := writeZip(t, dir, "sanhu-windows-amd64.zip", map[string][]byte{
		"sanhu.exe": want,
	})

	dest := filepath.Join(dir, "sanhu.new")
	if err := extractBinary(src, dest); err != nil {
		t.Fatalf("extractBinary: %v", err)
	}
	got, _ := os.ReadFile(dest)
	if !bytes.Equal(got, want) {
		t.Fatalf("zip 解压结果不一致")
	}
}

// 这就是原来的 bug：压缩包被原样写成 exe。体检必须拦住它。
func TestVerifyExecutableRejectsArchive(t *testing.T) {
	dir := t.TempDir()
	archive := writeTarGz(t, dir, "sanhu.tar.gz", map[string][]byte{
		"sanhu": fakeELF(2 << 20),
	})
	if err := verifyExecutable(archive); err == nil {
		t.Fatal("verifyExecutable 放行了 tar.gz —— 这正是会把安装搞坏的情况")
	}
}

func TestVerifyExecutableRejectsTiny(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "tiny")
	if err := os.WriteFile(p, fakeELF(1024), 0755); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := verifyExecutable(p); err == nil {
		t.Fatal("verifyExecutable 应拒绝过小的文件")
	}
}

func TestExtractBinaryMissingEntry(t *testing.T) {
	dir := t.TempDir()
	src := writeTarGz(t, dir, "sanhu-linux-amd64.tar.gz", map[string][]byte{
		"README.md": []byte("nothing here"),
	})
	if err := extractBinary(src, filepath.Join(dir, "out")); err == nil {
		t.Fatal("压缩包里没有二进制时应报错")
	}
}

func TestNeedsUpdate(t *testing.T) {
	cases := []struct {
		current, latest string
		want            bool
	}{
		{"v2.0.0", "v2.0.1", true},
		{"v2.0.0", "v2.1.0", true},
		{"v2.0.0", "v10.0.0", true}, // 字符串比较会判成 false
		{"v2.0.0", "v2.0.0", false},
		{"v2.0.1", "v2.0.0", false}, // 回滚发布不该把用户推去装旧版
		{"v2.0", "v2.0.0", false},   // 补零后相等
		{"dev", "v2.0.0", false},    // 开发版不更新
		{"", "v2.0.0", false},
		{"v2.0.0", "", false},
	}
	for _, c := range cases {
		if got := NeedsUpdate(c.current, c.latest); got != c.want {
			t.Errorf("NeedsUpdate(%q, %q) = %v, want %v", c.current, c.latest, got, c.want)
		}
	}
}

func TestAssetExt(t *testing.T) {
	cases := map[string]string{
		"https://x/sanhu-darwin-arm64.tar.gz": ".tar.gz",
		"https://x/sanhu-windows-amd64.zip":   ".zip",
		"https://x/seed.sql.gz":               ".gz",
		"https://x/sanhu-linux-amd64":         "",
		"https://x/sanhu.zip?token=abc":       ".zip",
	}
	for url, want := range cases {
		if got := assetExt(url); got != want {
			t.Errorf("assetExt(%q) = %q, want %q", url, got, want)
		}
	}
}
