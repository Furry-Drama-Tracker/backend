//go:build windows

package upload

// stripExecBits 在 Windows 上是空操作：Windows 没有 Unix 的 x 位，
// 可执行性由扩展名与 ACL 决定；os.Chmod 也只控制只读属性。
func stripExecBits(string) error {
	return nil
}
