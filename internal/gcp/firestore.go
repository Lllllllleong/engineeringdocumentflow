package gcp


import (
	"context"
	"fmt"

	"cloud.google.com/go/firestore"
)

// NewFirestoreClient creates and returns a new Firestore client for the given project ID.
// It centralizes client creation for all services.
func NewFirestoreClient(ctx context.Context, projectID string) (*firestore.Client, error) {
	if projectID == "" {
		return nil, fmt.Errorf("projectID must be provided to create a firestore client")
	}

	client, err := firestore.NewClient(ctx, projectID)
	if err != nil {
		return nil, fmt.Errorf("failed to create Firestore client: %w", err)
	}

	return client, nil
}