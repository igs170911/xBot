//go:build webembed

package web

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var files embed.FS

func dist() fs.FS {
	sub, err := fs.Sub(files, "dist")
	if err != nil {
		panic(err)
	}
	return sub
}
