//go:build !windows

package upload

import "os"

// stripExecBits 移除上传文件的可执行位（保留读写位，不改变 umask 语义）。
// 上传目录中的图片/SVG 永远不需要执行权限；即使文件被外部工具以 0755
// 模式放入，落盘后也会被纠正。失败时返回错误，调用方应删除该文件。
func stripExecBits(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	return os.Chmod(path, info.Mode().Perm()&^0o111)
}
