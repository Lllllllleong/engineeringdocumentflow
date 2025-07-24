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
)

var (
	aggregatorInstance *services.AggregatorFunction
	once               sync.Once
	initErr            error
)

func init() {
	// --- Set up structured logging ---
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	functions.HTTP("HandleAggregateMarkdown", handleAggregateMarkdown)
}

func main() {}

// handleAggregateMarkdown is the HTTP handler for the aggregation service.
func handleAggregateMarkdown(w http.ResponseWriter, r *http.Request) {
	once.Do(func() {
		aggregatorInstance, initErr = services.NewAggregator(context.Background())
	})
	if initErr != nil {
		slog.Error("Critical: Aggregator initialization failed", "error", initErr)
		http.Error(w, "Internal Server Error: failed to initialize service", http.StatusInternalServerError)
		return
	}

	var req models.MarkdownAggregatorRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		slog.Warn("Could not decode request body", "error", err)
		http.Error(w, "Bad Request: could not parse JSON", http.StatusBadRequest)
		return
	}

	res, err := aggregatorInstance.Process(r.Context(), &req)
	if err != nil {
		// Error is already logged with context in the Process method.
		http.Error(w, "Internal Server Error: processing failed", http.StatusInternalServerError)
		return
	}

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
