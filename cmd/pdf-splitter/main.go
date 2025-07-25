package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"sync"

	"github.com/GoogleCloudPlatform/functions-framework-go/functions"
	cloudevents "github.com/cloudevents/sdk-go/v2" // The official CloudEvents SDK
	"github.com/Lllllllleong/engineeringdocumentflow/internal/services"
	_ "github.com/GoogleCloudPlatform/functions-framework-go/functions"
)

var (
	pdfSplitterInstance *services.PDFSplitterFunction
	once                sync.Once
	initErr             error
)

func init() {
	// --- Set up structured logging ---
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

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
		// If initialization fails, log the fatal error and the function will terminate.
		slog.Error("Critical error during function initialization", "error", initErr)
		return initErr
	}

	// Unmarshal the event's data payload into our specific struct.
	var gcsEvent services.GCSEvent
	if err := json.Unmarshal(e.Data(), &gcsEvent); err != nil {
		slog.Error("Failed to unmarshal event data", "error", err, "data", string(e.Data()))
		return fmt.Errorf("json.Unmarshal: %w", err)
	}

	// Delegate the actual processing to our business logic method.
	err := pdfSplitterInstance.Process(ctx, gcsEvent)
	if err != nil {
		// The error is already logged with context within the Process method.
		// Returning it marks the function invocation as failed.
		return err
	}

	return nil
}
