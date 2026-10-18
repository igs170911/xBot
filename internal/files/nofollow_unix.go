//go:build unix

package files

import "syscall"

// nofollow：開檔時最後一段是連結就失敗。
const nofollow = syscall.O_NOFOLLOW
