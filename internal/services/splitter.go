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
	"cloud.google.com/go/workflows/executions/apiv1"
	"cloud.google.com/go/workflows/executions/apiv1/executionspb"
	// CORRECTED import path for shared models
	"github.com/Lllllllleong/engineeringdocumentflow/internal/models"
	"github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
	// CORRECTED typo in errgroup import
	"golang.org/x/sync/errgroup"
)

// RENAMED to be specific to the splitter service
type SplitterConfig struct {
	ProjectID        string
	SplitPagesBucket string
	CollectionName   string
	WorkflowID       string
	WorkflowLocation string
}

// RENAMED to be specific to the splitter service
type SplitterFunction struct {
	storageClient    *storage.Client
	firestoreClient  *firestore.Client
	executionsClient *executions.Client
	config           SplitterConfig // Uses the renamed config struct
}

// GCSEvent is the payload of a GCS event. This can stay here or be moved to models.
type GCSEvent struct {
	Bucket string `json:"bucket"`
	Name   string `json:"name"`
}

// RENAMED constructor to be specific. Called by main.go.
func NewSplitter(ctx context.Context) (*SplitterFunction, error) {
	projectID := os.Getenv("GCP_PROJECT")
	if projectID == "" {
		return nil, fmt.Errorf("GCP_PROJECT environment variable must be set")
	}

	config := SplitterConfig{
		ProjectID:        projectID,
		SplitPagesBucket: getEnv("SPLIT_PAGES_BUCKET", ""),
		CollectionName:   getEnv("FIRESTORE_COLLECTION", "documents"),
		WorkflowLocation: getEnv("WORKFLOW_LOCATION", "us-central1"),
		WorkflowID:       getEnv("WORKFLOW_ID", "document-processing-orchestrator"),
	}
	if config.SplitPagesBucket == "" {
		return nil, fmt.Errorf("SPLIT_PAGES_BUCKET must be set")
	}

	firestoreClient, err := firestore.NewClient(ctx, config.ProjectID)
	if err != nil {
		return nil, fmt.Errorf("failed to create Firestore client: %w", err)
	}
	storageClient, err := storage.NewClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create Storage client: %w", err)
	}
	executionsClient, err := executions.NewClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create Workflows Executions client: %w", err)
	}

	f := &SplitterFunction{
		firestoreClient:  firestoreClient,
		storageClient:    storageClient,
		executionsClient: executionsClient,
		config:           config,
	}
	log.Printf("Splitter logic initialized. Workflow to trigger: %s", config.WorkflowID)
	return f, nil
}

// Process contains the core business logic. It's called by the entry point in main.go.
func (f *SplitterFunction) Process(ctx context.Context, e GCSEvent) error {
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

func (f *SplitterFunction) isDuplicate(ctx context.Context, fileHash string) (bool, error) {
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

func (f *SplitterFunction) createInitialDocument(ctx context.Context, fileHash, filename string) (*firestore.DocumentRef, error) {
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

func (f *SplitterFunction) optimizeAndPrepare(ctx context.Context, docRef *firestore.DocumentRef, source, optimized string) (int, error) {
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

func (f *SplitterFunction) uploadSplitPages(ctx context.Context, docRef *firestore.DocumentRef, optimizedPdfPath string, pageCount int) error {
	log.Printf("Starting CONCURRENT upload of %d pages...", pageCount)
	eg, gctx := errgroup.WithContext(ctx)
	splitFileBase := optimizedPdfPath[:len(filepath.Ext(optimizedPdfPath))]
	for i := 1; i <= pageCount; i++ {
		pageNumber := i
		localSplitFilePath := fmt.Sprintf("%s_%d.pdf", splitFileBase, pageNumber)
		gcsDestObject := fmt.Sprintf("%s/%d.pdf", docRef.ID, pageNumber)
		eg.Go(func() error {
			if err := f.uploadFile(gctx, localSplitFilePath, gcsDestObject); err != nil {
				return fmt.Errorf("page %d: failed to upload: %w", pageNumber, err)
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

func (f *SplitterFunction) triggerWorkflow(ctx context.Context, docRef *firestore.DocumentRef, pageCount int) error {
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

func (f *SplitterFunction) handleError(ctx context.Context, docRef *firestore.DocumentRef, message string, originalErr error) error {
	fullError := fmt.Sprintf("%s: %v", message, originalErr)
	log.Printf("Error for doc %s: %s", docRef.ID, fullError)
	if err := f.updateStatus(ctx, docRef, "FAILED", fullError); err != nil {
		log.Printf("CRITICAL: Failed to update status to FAILED for doc %s. Update error: %v", docRef.ID, err)
	}
	return fmt.Errorf("%s", fullError)
}

func (f *SplitterFunction) updateStatus(ctx context.Context, docRef *firestore.DocumentRef, status, errDetails string) error {
	updates := []firestore.Update{
		{Path: "status", Value: status},
	}
	if errDetails != "" {
		updates = append(updates, firestore.Update{Path: "errorDetails", Value: errDetails})
	}
	_, err := docRef.Update(ctx, updates)
	return err
}

func (f *SplitterFunction) streamGCSObject(ctx context.Context, bucket, object, destPath string) error {
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

func (f *SplitterFunction) uploadFile(ctx context.Context, localPath, destObject string) error {
	localFileReader, err := os.Open(localPath)
	if err != nil {
		return fmt.Errorf("could not open local file %s: %w", localPath, err)
	}
	defer localFileReader.Close()
	gcsWriter := f.storageClient.Bucket(f.config.SplitPagesBucket).Object(destObject).NewWriter(ctx)
	defer gcsWriter.Close()
	if _, err := io.Copy(gcsWriter, localFileReader); err != nil {
		return fmt.Errorf("io.Copy to GCS failed: %w", err)
	}
	return nil
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

func getEnv(key, fallback string) string {
	if value, ok := os.LookupEnv(key); ok {
		return value
	}
	return fallback
}