package gcp

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"strings"

	"cloud.google.com/go/storage"
	"google.golang.org/api/googleapi"
		_ "github.com/GoogleCloudPlatform/functions-framework-go/functions"
)

// GetEnv is a helper to read an environment variable or return a default value.
func GetEnv(key, fallback string) string {
	if value, ok := os.LookupEnv(key); ok {
		return value
	}
	return fallback
}

// SaveToGCSAtomically writes content to a GCS object only if it doesn't already exist.
// It's a shared utility for all services.
func SaveToGCSAtomically(ctx context.Context, bucket *storage.BucketHandle, objectName, content string) error {
	writer := bucket.Object(objectName).If(storage.Conditions{DoesNotExist: true}).NewWriter(ctx)

	if _, err := io.Copy(writer, strings.NewReader(content)); err != nil {
		_ = writer.Close()
		if gerr, ok := err.(*googleapi.Error); ok && gerr.Code == 412 {
			log.Printf("SKIPPING: Object %s already exists.", objectName)
			return nil // Not a failure in an idempotent workflow.
		}
		log.Printf("ERROR: Failed to copy content to GCS object %s: %v", objectName, err)
		return fmt.Errorf("failed to write to GCS: %w", err)
	}

	if err := writer.Close(); err != nil {
		log.Printf("ERROR: Failed to close GCS writer for %s: %v", objectName, err)
		return fmt.Errorf("failed to finalize GCS write: %w", err)
	}
	return nil
}
