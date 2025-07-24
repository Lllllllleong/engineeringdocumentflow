package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync"

	"github.com/GoogleCloudPlatform/functions-framework-go/functions"
	cloudevents "github.com/cloudevents/sdk-go/v2" // The official CloudEvents SDK
	"github.com/Lllllllleong/engineeringdocumentflow/internal/services"
)

var (
	pdfSplitterInstance *services.PDFSplitterFunction
	once             sync.Once
	initErr          error
)

func init() {
	// Register the CloudEvent function. The framework will handle routing the event here.
	functions.CloudEvent("SplitAndPublish", splitAndPublish)
}

// main is required by the Go Functions Framework.
func main() {}

// splitAndPublish is the Cloud Function entry point.
// It now correctly accepts the standard cloudevents.Event type.
func splitAndPublish(ctx context.Context, e cloudevents.Event) error {
	// Use sync.Once for robust, one-time initialization of clients.
	once.Do(func() {
		pdfSplitterInstance, initErr = services.NewPDFSplitter(context.Background())
	})
	if initErr != nil {
		log.Fatalf("Critical error during function initialization: %v", initErr)
		return initErr
	}

	// This is the crucial new step: unmarshal the event's data payload
	// into the specific struct our business logic expects.
	var gcsEvent services.GCSEvent
	if err := json.Unmarshal(e.Data(), &gcsEvent); err != nil {
		log.Printf("ERROR: failed to unmarshal event data: %v", err)
		return fmt.Errorf("json.Unmarshal: %w", err)
	}

	// Now, delegate the actual processing to our clean business logic method,
	// passing the correctly typed and populated struct.
	err := pdfSplitterInstance.Process(ctx, gcsEvent)
	if err != nil {
		// The error is already logged within the Process method.
		// Returning it marks the function invocation as failed.
		return err
	}

	return nil
}