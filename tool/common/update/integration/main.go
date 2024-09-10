package main

import (
	"fmt"
	log "github.com/sirupsen/logrus"
	"os"

	"github.com/gravitational/trace"

	"github.com/gravitational/teleport/tool/common/update"
)

var Version = "development"

func main() {
	// At process startup, check if a version has already been downloaded to
	// $TELEPORT_HOME/bin or if the user has set the TELEPORT_TOOLS_VERSION
	// environment variable. If so, re-exec that version of {tsh, tctl}.
	toolsVersion, reExec := update.CheckLocal()
	if reExec {
		// Download the version of client tools required by the cluster. This
		// is required if the user passed in the TELEPORT_TOOLS_VERSION
		// explicitly.
		if err := update.Download(toolsVersion); err != nil {
			panic(trace.Wrap(err))
			return
		}

		// Re-execute client tools with the correct version of client tools.
		code, err := update.Exec()
		if err != nil {
			log.Fatalf("Failed to re-exec client tool: %v", err)
		} else {
			os.Exit(code)
		}
	}
	if len(os.Args) > 1 && os.Args[1] == "version" {
		fmt.Println("Teleport v", Version)
	}
}
