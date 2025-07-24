package splitter

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
	"github.com/Lllllllleong/engineeringdocumentflow/orchestration/internal/models" // <-- IMPORTING OUR SHARED MODEL
	"github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"

	executions "cloud.google.com/go/workflows/executions/apiv1"
	"cloud.google.com/go/workflows/executions/apiv1/executionspb"
)

// Config holds configuration settings read from the environment.
type Config struct {
	ProjectID        string
	SplitPagesBucket string
	CollectionName   string
	WorkflowID       string
	WorkflowLocation string
}

// Function holds the dependencies for our cloud function logic.
type Function struct {
	storageClient    *storage.Client
	firestoreClient  *firestore.Client
	executionsClient *executions.Client
	config           Config
}

// GCSEvent is the payload of a GCS event.
type GCSEvent struct {
	Bucket string `json:"bucket"`
	Name   string `json:"name"`
}

// New creates a new Function instance with all dependencies initialized.
func New(ctx context.Context) (*Function, error) {
	projectID := os.Getenv("GCP_PROJECT")
	if projectID == "" {
		return nil, fmt.Errorf("GCP_PROJECT environment variable must be set")
	}

	config := Config{
		ProjectID:        projectID,
		SplitPagesBucket: getEnv("SPLIT_PAGES_BUCKET", ""),
		CollectionName:   getEnv("FIRESTORE_COLLECTION", "documents"),
		WorkflowLocation: getEnv("WORKFLOW_LOCATION", "us-central1"),
		WorkflowID:       getEnv("WORKFLOW_ID", "document-processing-orchestrator"),
	}
	// ... (add other config checks if necessary)

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

	f := &Function{
		firestoreClient:  firestoreClient,
		storageClient:    storageClient,
		executionsClient: executionsClient,
		config:           config,
	}
	log.Printf("Splitter logic initialized. Workflow to trigger: %s", config.WorkflowID)
	return f, nil
}

// Process is the main business logic handler for the splitter service.
func (f *Function) Process(ctx context.Context, e GCSEvent) error {
	tempDir, err := os.MkdirTemp("", "pdf-splitter-*")
	if err != nil {
		return fmt.Errorf("failed to create temp dir: %w", err)
	}
	defer os.RemoveAll(tempDir)
	log.Printf("Created temp directory: %s", tempDir)

	sourcePdfPath := filepath.Join(tempDir, "source.pdf")
	if err := f.streamGCSObject(ctx, e.Bucket, e.Name, sourcePdfPath); err != nil {
		return err
	}

	fileHash, err := calculateFileHash(sourcePdfPath)
	if err != nil {
		return fmt.Errorf("failed to calculate file hash: %w", err)
	}

	isDuplicate, err := f.isDuplicate(ctx, fileHash)
	if err != nil || isDuplicate {
		return err // Stop if error or if it's a duplicate
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

	if err := f.uploadSplitPages(ctx, docRef, optimizedPdfPath, pageCount); err != nil {
		return err
	}

	if err := f.triggerWorkflow(ctx, docRef, pageCount); err != nil {
		return err
	}

	log.Printf("Hand-off to workflow complete for document %s.", docRef.ID)
	return nil
}

func (f *Function) isDuplicate(ctx context.Context, fileHash string) (bool, error) {
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

func (f *Function) createInitialDocument(ctx context.Context, fileHash, filename string) (*firestore.DocumentRef, error) {
	newDoc := models.Document{ // <-- USING THE SHARED MODEL
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

func (f *Function) optimizeAndPrepare(ctx context.Context, docRef *firestore.DocumentRef, source, optimized string) (int, error) {
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

func (f *Function) uploadSplitPages(ctx context.Context, docRef *firestore.DocumentRef, optimizedPdfPath string, pageCount int) error {
	log.Printf("Starting serial upload of %d pages...", pageCount)
	splitFileBase := optimizedPdfPath[:len(optimizedPdfPath)-len(filepath.Ext(optimizedPdfPath))]
	for i := 1; i <= pageCount; i++ {
		localSplitFilePath := fmt.Sprintf("%s_%d.pdf", splitFileBase, i)
		gcsDestObject := fmt.Sprintf("%s/%d.pdf", docRef.ID, i)
		if err := f.uploadFile(ctx, localSplitFilePath, gcsDestObject); err != nil {
			return f.handleError(ctx, docRef, fmt.Sprintf("page %d: failed to upload", i), err)
		}
	}
	return nil
}

func (f *Function) triggerWorkflow(ctx context.Context, docRef *firestore.DocumentRef, pageCount int) error {
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

func (f *Function) handleError(ctx context.Context, docRef *firestore.DocumentRef, message string, originalErr error) error {
	fullError := fmt.Sprintf("%s: %v", message, originalErr)
	log.Printf("Error for doc %s: %s", docRef.ID, fullError)
	if err := f.updateStatus(ctx, docRef, "FAILED", fullError); err != nil {
		log.Printf("CRITICAL: Failed to update status to FAILED for doc %s. Update error: %v", docRef.ID, err)
	}
	return fmt.Errorf(fullError)
}

func (f *Function) updateStatus(ctx context.Context, docRef *firestore.DocumentRef, status, errDetails string) error {
	updates := []firestore.Update{
		{Path: "status", Value: status},
	}
	if errDetails != "" {
		updates = append(updates, firestore.Update{Path: "errorDetails", Value: errDetails})
	}
	_, err := docRef.Update(ctx, updates)
	return err
}

func (f *Function) streamGCSObject(ctx context.Context, bucket, object, destPath string) error {
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
	log.Printf("Successfully streamed GCS file to %s", destPath)
	return nil
}

func optimizePDF(inPath, outPath string) error {
	cfg := model.NewDefaultConfiguration()
	cfg.ValidationMode = model.ValidationRelaxed
	return api.OptimizeFile(inPath, outPath, cfg)
}

func (f *Function) uploadFile(ctx context.Context, localPath, destObject string) error {
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
