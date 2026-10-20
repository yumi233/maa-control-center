package ui

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var files embed.FS

func Files() fs.FS { f, _ := fs.Sub(files, "dist"); return f }
