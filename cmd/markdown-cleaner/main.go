package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"sync"

	"github.com/GoogleCloudPlatform/functions-framework-go/functions"
	"github.com/Lllllllleong/engineeringdocumentflow/internal/models"
	"github.com/Lllllllleong/engineeringdocumentflow/internal/services"
)

var (
	cleanerInstance *services.CleanerFunction
	once            sync.Once
	initErr         error
)

func init() {
	// Register the HTTP function with the framework.
	// "HandleCleanMarkdown" is the entry point name configured in GCP.
	functions.HTTP("HandleCleanMarkdown", handleCleanMarkdown)
}

// main is required by the Go Functions Framework.
func main() {}

// handleCleanMarkdown is the HTTP handler for the cleanup service.
func handleCleanMarkdown(w http.ResponseWriter, r *http.Request) {
	// Use sync.Once for robust, one-time initialization of clients.
	once.Do(func() {
		cleanerInstance, initErr = services.NewCleaner(context.Background())
	})
	if initErr != nil {
		log.Printf("CRITICAL: Cleaner initialization failed: %v", initErr)
		http.Error(w, "Internal Server Error: failed to initialize service", http.StatusInternalServerError)
		return
	}

	// Decode the incoming JSON request from the workflow.
	var req models.MarkdownCleanerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		log.Printf("ERROR: Could not decode request body: %v", err)
		http.Error(w, "Bad Request: could not parse JSON", http.StatusBadRequest)
		return
	}

	// Delegate to the business logic.
	res, err := cleanerInstance.Process(r.Context(), &req)
	if err != nil {
		// The specific error is already logged inside the Process method.
		http.Error(w, "Internal Server Error: processing failed", http.StatusInternalServerError)
		return
	}

	// If successful, encode the response and send it back to the workflow.
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(res); err != nil {
		log.Printf("ERROR: Failed to write response: %v", err)
		http.Error(w, "Internal Server Error: failed to encode response", http.StatusInternalServerError)
	}
}