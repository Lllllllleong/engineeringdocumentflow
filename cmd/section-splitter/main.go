package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"sync"

	"github.com/GoogleCloudPlatform/functions-framework-go/functions"
	"github.com/Lllllllleong/engineeringdocumentflow/internal/models"
	"github.com/Lllllllleong/engineeringdocumentflow/internal/services"
	_ "github.com/GoogleCloudPlatform/functions-framework-go/functions"
)

var (
	splitterInstance *services.SectionSplitterFunction
	once             sync.Once
	initErr          error
)

func init() {
	// --- Set up structured logging ---
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	// Register the HTTP function with the framework.
	// "HandleSplitSections" is the entry point name configured in GCP.
	functions.HTTP("HandleSplitSections", handleSplitSections)
}

// main is required by the Go Functions Framework.
func main() {}

// handleSplitSections is the HTTP handler for the section splitting service.
func handleSplitSections(w http.ResponseWriter, r *http.Request) {
	// Use sync.Once for robust, one-time initialization of clients.
	once.Do(func() {
		// This now calls the correctly named constructor.
		splitterInstance, initErr = services.NewSectionSplitter(context.Background())
	})
	if initErr != nil {
		slog.Error("Critical: SectionSplitter initialization failed", "error", initErr)
		http.Error(w, "Internal Server Error: failed to initialize service", http.StatusInternalServerError)
		return
	}

	// Decode the incoming JSON request from the workflow.
	var req models.SectionSplitterRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		slog.Warn("Could not decode request body", "error", err)
		http.Error(w, "Bad Request: could not parse JSON", http.StatusBadRequest)
		return
	}

	// Delegate to the business logic.
	res, err := splitterInstance.Process(r.Context(), &req)
	if err != nil {
		// The specific error is already logged inside the Process method.
		http.Error(w, "Internal Server Error: processing failed", http.StatusInternalServerError)
		return
	}

	// If successful, encode the response and send it back to the workflow.
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(res); err != nil {
		slog.Error(
			"Failed to write response",
			"error", err,
			"documentId", req.DocumentID,
			"executionId", req.ExecutionID,
		)
		http.Error(w, "Internal Server Error: failed to encode response", http.StatusInternalServerError)
	}
}
