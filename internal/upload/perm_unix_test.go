//go:build !windows

package upload

import (
	"os"
	"path"
	"strings"
	"testing"
)

// assertNoExec 断言文件没有 Unix 可执行位。
func assertNoExec(t *testing.T, p string) {
	t.Helper()
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatalf("stat %s: %v", p, err)
	}
	if fi.Mode().Perm()&0o111 != 0 {
		t.Fatalf("file %s has exec bits: %v", p, fi.Mode())
	}
}

func TestStripExecBits(t *testing.T) {
	p := path.Join(t.TempDir(), "icon.svg")
	if err := os.WriteFile(p, []byte("<svg/>"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.Chmod(p, 0o755); err != nil {
		t.Fatalf("chmod 0755: %v", err)
	}
	if err := stripExecBits(p); err != nil {
		t.Fatalf("stripExecBits: %v", err)
	}
	assertNoExec(t, p)
}

func TestSaveImageNoExecBits(t *testing.T) {
	setupTest(t)
	req := multipartRequest(t, "image", "photo.jpg", "image/jpeg", jpegBytes)
	url, err := SaveImage(newGinContext(req), "image", "avatar", 5<<20)
	if err != nil {
		t.Fatalf("SaveImage: %v", err)
	}
	assertNoExec(t, path.Join(Dir, strings.TrimPrefix(url, "/uploads/")))
}

func TestSaveSVGNoExecBits(t *testing.T) {
	old := IconsDir
	SetIconsDir(t.TempDir())
	t.Cleanup(func() { IconsDir = old })

	req := multipartRequest(t, "file", "icon.svg", "image/svg+xml",
		[]byte(`<svg xmlns="http://www.w3.org/2000/svg"></svg>`))
	url, err := SaveSVG(newGinContext(req), "file")
	if err != nil {
		t.Fatalf("SaveSVG: %v", err)
	}
	assertNoExec(t, path.Join(IconsDir, strings.TrimPrefix(url, "/uploads/icons/")))
}
