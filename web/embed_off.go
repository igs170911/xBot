//go:build !webembed

package web

import "io/fs"

func dist() fs.FS { return nil }
