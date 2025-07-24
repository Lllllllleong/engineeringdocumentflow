package main

import (

	"github.com/GoogleCloudPlatform/functions-framework-go/functions"
	// This is the key: we import our own code using the module path
	"github.com/Lllllllleong/engineeringdocumentflow/internal/splitter"
)

func init() {
	// Register the event-driven function with the Functions Framework
	functions.CloudEvent("SplitAndPublish", splitter.SplitAndPublish)
}

// main is needed for the Go Functions Framework to start the server.
func main() {}
