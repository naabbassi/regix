// Package web holds the built user interface (see /ui), compiled into the binary.
package web

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var dist embed.FS

// Assets is the root of the built user interface.
func Assets() fs.FS {
	assets, err := fs.Sub(dist, "dist")
	if err != nil {
		panic(err) // the directory is embedded above
	}
	return assets
}
