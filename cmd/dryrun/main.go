// Copyright Contributors to the Open Cluster Management project

package main

import (
	"errors"
	"os"

	"open-cluster-management.io/config-policy-controller/pkg/dryrun"
	"open-cluster-management.io/config-policy-controller/pkg/dryrun/server"
	webui "open-cluster-management.io/config-policy-controller/web"
)

func main() {
	runner := dryrun.DryRunner{}
	cmd := runner.GetCmd()

	// serve is registered here rather than in pkg/dryrun/cmd.go to avoid an import cycle
	// between pkg/dryrun and pkg/dryrun/server.
	cmd.AddCommand(server.NewCommand(&server.Config{
		StaticFS: webui.Dist(),
	}))

	err := cmd.Execute()
	if errors.Is(err, dryrun.ErrNonCompliant) {
		os.Exit(2)
	}

	if err != nil {
		os.Exit(1)
	}
}
