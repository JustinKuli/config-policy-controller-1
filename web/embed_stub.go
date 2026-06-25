// Copyright Contributors to the Open Cluster Management project

//go:build !embedui

package webui

import "io/fs"

// Dist returns nil in builds without the embedui tag.
func Dist() fs.FS {
	return nil
}
