package services // CHANGED package name to 'services'

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"time"

	"cloud.google.com/go/firestore"
	"cloud.google.com/go/storage"
	executions "cloud.google.com/go/workflows/executions/apiv1"
	"cloud.google.com/go/workflows/executions/apiv1/executionspb"

	// CORRECTED import path for shared models
	"github.com/Lllllllleong/engineeringdocumentflow/internal/gcp"
	"github.com/Lllllllleong/engineeringdocumentflow/internal/models"
	"github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"

	// CORRECTED typo in errgroup import
	"golang.org/x/sync/errgroup"
)

// RENAMED to be specific to the splitter service
type PDFSplitterConfig struct {
	ProjectID        string
	SplitPagesBucket string
	CollectionName   string
	WorkflowID       string
	WorkflowLocation string
}

// RENAMED to be specific to the splitter service
type PDFSplitterFunction struct {
	storageClient    *storage.Client
	firestoreClient  *firestore.Client
	executionsClient *executions.Client
	config           PDFSplitterConfig // Uses the renamed config struct
}

// GCSEvent is the payload of a GCS event. This can stay here or be moved to models.
type GCSEvent struct {
	Bucket string `json:"bucket"`
	Name   string `json:"name"`
}

// RENAMED constructor to be specific. Called by main.go.
func NewPDFSplitter(ctx context.Context) (*PDFSplitterFunction, error) {
	projectID := gcp.GetEnv("PROJECT_ID", "") // Use the shared helper
	if projectID == "" {
		return nil, fmt.Errorf("GCP_PROJECT environment variable must be set")
	}

	config := PDFSplitterConfig{
		ProjectID:        projectID,
		SplitPagesBucket: gcp.GetEnv("SPLIT_PAGES_BUCKET", ""),
		CollectionName:   gcp.GetEnv("FIRESTORE_COLLECTION", "documents"),
		WorkflowLocation: gcp.GetEnv("WORKFLOW_LOCATION", "us-central1"),
		WorkflowID:       gcp.GetEnv("WORKFLOW_ID", "document-processing-orchestrator"),
	}
	if config.SplitPagesBucket == "" {
		return nil, fmt.Errorf("SPLIT_PAGES_BUCKET environment variable must be set")
	}

	firestoreClient, err := gcp.NewFirestoreClient(ctx, config.ProjectID) // Use the centralized factory
	if err != nil {
		return nil, fmt.Errorf("failed to create firestore client: %w", err)
	}
	storageClient, err := storage.NewClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create Storage client: %w", err)
	}
	executionsClient, err := executions.NewClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create Workflows Executions client: %w", err)
	}

	f := &PDFSplitterFunction{
		firestoreClient:  firestoreClient,
		storageClient:    storageClient,
		executionsClient: executionsClient,
		config:           config,
	}
	log.Printf("Splitter logic initialized. Workflow to trigger: %s", config.WorkflowID)
	return f, nil
}

// Process contains the core business logic. It's called by the entry point in main.go.
func (f *PDFSplitterFunction) Process(ctx context.Context, e GCSEvent) error {
	tempDir, err := os.MkdirTemp("", "pdf-splitter-*")
	if err != nil {
		return fmt.Errorf("failed to create temp dir: %w", err)
	}
	defer os.RemoveAll(tempDir)
	log.Printf("Created temp directory: %s", tempDir)

	sourcePdfPath := filepath.Join(tempDir, "source.pdf")
	if err := f.streamGCSObject(ctx, e.Bucket, e.Name, sourcePdfPath); err != nil {
		return err // Errors are returned up to the main entry point
	}

	fileHash, err := calculateFileHash(sourcePdfPath)
	if err != nil {
		return fmt.Errorf("failed to calculate file hash: %w", err)
	}

	isDuplicate, err := f.isDuplicate(ctx, fileHash)
	if err != nil || isDuplicate {
		return err // Stop if error or if it's a clean exit for a duplicate
	}

	docRef, err := f.createInitialDocument(ctx, fileHash, e.Name)
	if err != nil {
		return err
	}
	log.Printf("Created master document with ID: %s", docRef.ID)

	optimizedPdfPath := filepath.Join(tempDir, "optimized.pdf")
	pageCount, err := f.optimizeAndPrepare(ctx, docRef, sourcePdfPath, optimizedPdfPath)
	if err != nil {
		return err
	}

	// Your concurrent upload logic is correct and preserved here.
	if err := f.uploadSplitPages(ctx, docRef, optimizedPdfPath, pageCount); err != nil {
		return err
	}

	if err := f.triggerWorkflow(ctx, docRef, pageCount); err != nil {
		return err
	}

	log.Printf("Hand-off to workflow complete for document %s.", docRef.ID)
	return nil
}

// NOTE: All helper functions below this line were correct.
// I have included them here for completeness. No changes were needed to them.

func (f *PDFSplitterFunction) isDuplicate(ctx context.Context, fileHash string) (bool, error) {
	docs, err := f.firestoreClient.Collection(f.config.CollectionName).Where("fileHash", "==", fileHash).Limit(1).Documents(ctx).GetAll()
	if err != nil {
		return false, fmt.Errorf("failed to query for duplicates: %w", err)
	}
	if len(docs) > 0 {
		log.Printf("Duplicate file detected (hash: %s). Skipping. Doc ID: %s", fileHash, docs[0].Ref.ID)
		return true, nil
	}
	return false, nil
}

func (f *PDFSplitterFunction) createInitialDocument(ctx context.Context, fileHash, filename string) (*firestore.DocumentRef, error) {
	newDoc := models.Document{ // Uses the shared model
		FileHash:         fileHash,
		OriginalFilename: filename,
		Status:           "VALIDATING",
		CreatedAt:        time.Now(),
	}
	docRef, _, err := f.firestoreClient.Collection(f.config.CollectionName).Add(ctx, newDoc)
	if err != nil {
		return nil, fmt.Errorf("failed to create master document: %w", err)
	}
	return docRef, nil
}

func (f *PDFSplitterFunction) optimizeAndPrepare(ctx context.Context, docRef *firestore.DocumentRef, source, optimized string) (int, error) {
	if err := optimizePDF(source, optimized); err != nil {
		return 0, f.handleError(ctx, docRef, "failed to validate/optimize PDF", err)
	}
	pageCount, err := api.PageCountFile(optimized)
	if err != nil {
		return 0, f.handleError(ctx, docRef, "failed to get page count", err)
	}
	if err := api.SplitFile(optimized, filepath.Dir(optimized), 1, nil); err != nil {
		return 0, f.handleError(ctx, docRef, "failed to split PDF", err)
	}
	updates := []firestore.Update{
		{Path: "status", Value: "SPLITTING"},
		{Path: "pageCount", Value: pageCount},
	}
	if _, err := docRef.Update(ctx, updates); err != nil {
		return 0, f.handleError(ctx, docRef, "failed to update status to SPLITTING", err)
	}
	return pageCount, nil
}

func (f *PDFSplitterFunction) uploadSplitPages(ctx context.Context, docRef *firestore.DocumentRef, optimizedPdfPath string, pageCount int) error {
	log.Printf("Starting CONCURRENT upload of %d pages...", pageCount)
	eg, gctx := errgroup.WithContext(ctx)
	// Limit the number of concurrent uploads to avoid overwhelming resources
	eg.SetLimit(10)

	splitFileBase := strings.TrimSuffix(optimizedPdfPath, filepath.Ext(optimizedPdfPath))

	for i := 1; i <= pageCount; i++ {
		// These variables are captured by the goroutine, so they need to be
		// created inside the loop scope.
		pageNumber := i
		localSplitFilePath := fmt.Sprintf("%s_%d.pdf", splitFileBase, pageNumber)
		gcsDestObject := fmt.Sprintf("%s/%05d.pdf", docRef.ID, pageNumber)

		eg.Go(func() error {
			if err := f.uploadFile(gctx, localSplitFilePath, gcsDestObject); err != nil {
				// The error from uploadFile is already detailed.
				return fmt.Errorf("page %d: %w", pageNumber, err)
			}
			return nil
		})
	}
	if err := eg.Wait(); err != nil {
		return f.handleError(ctx, docRef, "one or more pages failed to upload", err)
	}
	log.Printf("All %d pages uploaded successfully.", pageCount)
	return nil
}

func (f *PDFSplitterFunction) triggerWorkflow(ctx context.Context, docRef *firestore.DocumentRef, pageCount int) error {
	log.Printf("Triggering workflow '%s' for document ID %s", f.config.WorkflowID, docRef.ID)
	workflowPayload := map[string]interface{}{
		"documentId": docRef.ID,
		"pageCount":  pageCount,
	}
	payloadBytes, err := json.Marshal(workflowPayload)
	if err != nil {
		return f.handleError(ctx, docRef, "failed to marshal workflow payload", err)
	}
	req := &executionspb.CreateExecutionRequest{
		Parent: fmt.Sprintf("projects/%s/locations/%s/workflows/%s", f.config.ProjectID, f.config.WorkflowLocation, f.config.WorkflowID),
		Execution: &executionspb.Execution{
			Argument: string(payloadBytes),
		},
	}
	_, err = f.executionsClient.CreateExecution(ctx, req)
	if err != nil {
		return f.handleError(ctx, docRef, "failed to trigger workflow execution", err)
	}
	return nil
}

func (f *PDFSplitterFunction) handleError(ctx context.Context, docRef *firestore.DocumentRef, message string, originalErr error) error {
	fullError := fmt.Sprintf("%s: %v", message, originalErr)
	log.Printf("Error for doc %s: %s", docRef.ID, fullError)
	if err := f.updateStatus(ctx, docRef, "FAILED", fullError); err != nil {
		log.Printf("CRITICAL: Failed to update status to FAILED for doc %s. Update error: %v", docRef.ID, err)
	}
	return fmt.Errorf("%s", fullError)
}

func (f *PDFSplitterFunction) updateStatus(ctx context.Context, docRef *firestore.DocumentRef, status, errDetails string) error {
	updates := []firestore.Update{
		{Path: "status", Value: status},
	}
	if errDetails != "" {
		updates = append(updates, firestore.Update{Path: "errorDetails", Value: errDetails})
	}
	_, err := docRef.Update(ctx, updates)
	return err
}

func (f *PDFSplitterFunction) streamGCSObject(ctx context.Context, bucket, object, destPath string) error {
	gcsReader, err := f.storageClient.Bucket(bucket).Object(object).NewReader(ctx)
	if err != nil {
		return fmt.Errorf("failed to get GCS object reader for gs://%s/%s: %w", bucket, object, err)
	}
	defer gcsReader.Close()
	localFile, err := os.Create(destPath)
	if err != nil {
		return fmt.Errorf("failed to create temp file at %s: %w", destPath, err)
	}
	defer localFile.Close()
	if _, err := io.Copy(localFile, gcsReader); err != nil {
		return fmt.Errorf("failed to copy GCS object to local file: %w", err)
	}
	return nil
}

func optimizePDF(inPath, outPath string) error {
	cfg := model.NewDefaultConfiguration()
	cfg.ValidationMode = model.ValidationRelaxed
	return api.OptimizeFile(inPath, outPath, cfg)
}

// uploadFile attempts to upload a single file to GCS with retries and exponential backoff.
func (f *PDFSplitterFunction) uploadFile(ctx context.Context, localPath, destObject string) error {
	const maxRetries = 4
	var backoff = 1 * time.Second
	var lastErr error

	for i := 0; i < maxRetries; i++ {
		// This inner function allows us to use defer for cleanup on each attempt.
		err := func() error {
			localFileReader, err := os.Open(localPath)
			if err != nil {
				return fmt.Errorf("could not open local file %s: %w", localPath, err)
			}
			defer localFileReader.Close()

			// Use a timeout for each write attempt to avoid getting stuck.
			writeCtx, cancel := context.WithTimeout(ctx, time.Second*50)
			defer cancel()

			gcsWriter := f.storageClient.Bucket(f.config.SplitPagesBucket).Object(destObject).NewWriter(writeCtx)

			if _, err := io.Copy(gcsWriter, localFileReader); err != nil {
				_ = gcsWriter.Close() // Attempt to clean up writer
				return fmt.Errorf("io.Copy to GCS failed: %w", err)
			}

			// Close() is the operation that finalizes the upload. It's critical to check its error.
			if err := gcsWriter.Close(); err != nil {
				return fmt.Errorf("failed to close GCS writer (finalize upload): %w", err)
			}
			return nil
		}()

		if err == nil {
			return nil // Success!
		}

		// If the attempt failed, log it and prepare for the next retry.
		lastErr = err
		log.Printf("WARN: Upload for %s failed (attempt %d/%d): %v. Retrying in %v...", destObject, i+1, maxRetries, backoff)

		// Wait for the backoff period, but stop if the function's context is cancelled.
		select {
		case <-time.After(backoff):
			backoff *= 2 // Double the wait time for the next attempt.
		case <-ctx.Done():
			log.Printf("ERROR: Context cancelled during backoff for %s. Aborting retries.", destObject)
			return ctx.Err()
		}
	}

	log.Printf("ERROR: Upload for %s failed after %d attempts.", destObject, maxRetries)
	return fmt.Errorf("upload for %s failed after all retries: %w", destObject, lastErr)
}

func calculateFileHash(filePath string) (string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}
