package services

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"sort"
	"strings"

	"cloud.google.com/go/storage"
	"github.com/Lllllllleong/engineeringdocumentflow/internal/gcp"
	"github.com/Lllllllleong/engineeringdocumentflow/internal/models"
	"google.golang.org/api/iterator"
)

// AggregatorConfig holds configuration for the aggregator service.
type AggregatorConfig struct {
	ProjectID                string
	TranslatedMarkdownBucket string
	AggregatedMarkdownBucket string
}

// AggregatorFunction holds dependencies for the aggregation logic.
type AggregatorFunction struct {
	storageClient *storage.Client
	config        AggregatorConfig
}

// NewAggregator creates a new AggregatorFunction instance.
func NewAggregator(ctx context.Context) (*AggregatorFunction, error) {
	projectID := gcp.GetEnv("PROJECT_ID", "")
	if projectID == "" {
		return nil, fmt.Errorf("GCP_PROJECT environment variable must be set")
	}

	config := AggregatorConfig{
		ProjectID:                projectID,
		TranslatedMarkdownBucket: gcp.GetEnv("TRANSLATED_MARKDOWN_BUCKET", ""), // Source bucket
		AggregatedMarkdownBucket: gcp.GetEnv("AGGREGATED_MARKDOWN_BUCKET", ""), // Destination bucket
	}
	if config.TranslatedMarkdownBucket == "" || config.AggregatedMarkdownBucket == "" {
		return nil, fmt.Errorf("TRANSLATED_MARKDOWN_BUCKET and AGGREGATED_MARKDOWN_BUCKET must be set")
	}

	storageClient, err := storage.NewClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create storage client: %w", err)
	}

	return &AggregatorFunction{
		storageClient: storageClient,
		config:        config,
	}, nil
}

// Process handles the core logic of aggregating Markdown files.
func (f *AggregatorFunction) Process(ctx context.Context, req *models.MarkdownAggregatorRequest) (*models.MarkdownAggregatorResponse, error) {
	logCtx := slog.With("documentId", req.DocumentID, "executionId", req.ExecutionID)
	logCtx.Info("Starting aggregation.")

	// --- 1. List all .md files for the documentId ---
	query := &storage.Query{Prefix: req.DocumentID + "/"}
	it := f.storageClient.Bucket(f.config.TranslatedMarkdownBucket).Objects(ctx, query)

	var objectNames []string
	for {
		attrs, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			logCtx.Error("Failed to list objects in source bucket", "error", err, "bucket", f.config.TranslatedMarkdownBucket)
			return nil, fmt.Errorf("failed to list markdown files: %w", err)
		}
		if strings.HasSuffix(attrs.Name, ".md") {
			objectNames = append(objectNames, attrs.Name)
		}
	}

	if len(objectNames) == 0 {
		logCtx.Warn("No markdown files found to aggregate. This might be an error or an empty document.")
		// We proceed to create an empty master file for consistency downstream.
	}

	// --- 2. Sort the filenames to ensure correct page order ---
	sort.Strings(objectNames)
	logCtx.Info("Found and sorted files for aggregation.", "fileCount", len(objectNames))

	// --- 3. Stream-concatenate files with centralized error handling ---
	outputObjectName := fmt.Sprintf("%s/master.md", req.DocumentID)
	destWriter := f.storageClient.Bucket(f.config.AggregatedMarkdownBucket).Object(outputObjectName).NewWriter(ctx)
	var aggregationErr error

	for _, objName := range objectNames {
		logCtx.Info("Appending page.", "gcsObject", objName)
		sourceReader, err := f.storageClient.Bucket(f.config.TranslatedMarkdownBucket).Object(objName).NewReader(ctx)
		if err != nil {
			aggregationErr = fmt.Errorf("failed to read %s: %w", objName, err)
			break // Exit the loop on error
		}

		if _, err := io.Copy(destWriter, sourceReader); err != nil {
			sourceReader.Close()
			aggregationErr = fmt.Errorf("failed to copy content from %s: %w", objName, err)
			break // Exit the loop on error
		}
		sourceReader.Close() // Close successful reader

		// Add a separator between files.
		if _, err := destWriter.Write([]byte("\n\n---\n\n")); err != nil {
			aggregationErr = fmt.Errorf("failed to write separator: %w", err)
			break // Exit the loop on error
		}
	}

	if err := destWriter.Close(); err != nil {
		logCtx.Error("Critical: Failed to finalize master.md write", "error", err, "object", outputObjectName)
		return nil, fmt.Errorf("failed to finalize master.md: %w", err)
	}

	if aggregationErr != nil {
		logCtx.Error("Error during aggregation loop", "error", aggregationErr)
		return nil, aggregationErr
	}

	logCtx.Info("Aggregation complete.")

	// --- 4. Return the URI of the new master file ---
	outputGCSUri := fmt.Sprintf("gs://%s/%s", f.config.AggregatedMarkdownBucket, outputObjectName)
	return &models.MarkdownAggregatorResponse{
		Status:       "success",
		MasterGCSUri: outputGCSUri,
	}, nil
}
