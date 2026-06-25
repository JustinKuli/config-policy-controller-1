// Copyright Contributors to the Open Cluster Management project

//go:build embedui

package webui

import (
	"embed"
	"io/fs"
)

//go:embed dist/*
var dist embed.FS

// Dist returns the embedded production web UI assets. It is only available in
// binaries built with the embedui tag after running `npm run build` in web/.
func Dist() fs.FS {
	return dist
}
