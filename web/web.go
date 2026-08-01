// Package web holds embedded static assets, served under /static/.
package web

import (
	"embed"
	"io/fs"
)

//go:embed static
var embedded embed.FS

// Static is the static/ directory rooted at its own contents.
var Static = func() fs.FS {
	sub, err := fs.Sub(embedded, "static")
	if err != nil {
		panic(err)
	}
	return sub
}()
