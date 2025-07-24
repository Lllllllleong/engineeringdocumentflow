package services

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"cloud.google.com/go/firestore"
	"cloud.google.com/go/storage"
	executions "cloud.google.com/go/workflows/executions/apiv1"
	"cloud.google.com/go/workflows/executions/apiv1/executionspb"
	"github.com/Lllllllleong/engineeringdocumentflow/internal/gcp"
	"github.com/Lllllllleong/engineeringdocumentflow/internal/models"
	"github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
	"golang.org/x/sync/errgroup"
)

type PDFSplitterConfig struct {
	ProjectID        string
	SplitPagesBucket string
	CollectionName   string
	WorkflowID       string
	WorkflowLocation string
}

type PDFSplitterFunction struct {
	storageClient    *storage.Client
	firestoreClient  *firestore.Client
	executionsClient *executions.Client
	config           PDFSplitterConfig
}

type GCSEvent struct {
	Bucket string `json:"bucket"`
	Name   string `json:"name"`
}

func NewPDFSplitter(ctx context.Context) (*PDFSplitterFunction, error) {
	projectID := gcp.GetEnv("PROJECT_ID", "")
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

	firestoreClient, err := gcp.NewFirestoreClient(ctx, config.ProjectID)
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
	slog.Info("PDF Splitter logic initialized.", "workflowId", config.WorkflowID)
	return f, nil
}

func (f *PDFSplitterFunction) Process(ctx context.Context, e GCSEvent) error {
	logCtx := slog.With("gcsBucket", e.Bucket, "gcsObject", e.Name)
	logCtx.Info("Processing new GCS object.")

	tempDir, err := os.MkdirTemp("", "pdf-splitter-*")
	if err != nil {
		return fmt.Errorf("failed to create temp dir: %w", err)
	}
	defer os.RemoveAll(tempDir)
	logCtx.Info("Created temp directory.", "path", tempDir)

	sourcePdfPath := filepath.Join(tempDir, "source.pdf")
	if err := f.streamGCSObject(ctx, e.Bucket, e.Name, sourcePdfPath); err != nil {
		logCtx.Error("Failed to download source PDF", "error", err)
		return err
	}

	fileHash, err := calculateFileHash(sourcePdfPath)
	if err != nil {
		logCtx.Error("Failed to calculate file hash", "error", err)
		return fmt.Errorf("failed to calculate file hash: %w", err)
	}
	logCtx = logCtx.With("fileHash", fileHash)

	isDuplicate, docID, err := f.isDuplicate(ctx, fileHash)
	if err != nil {
		logCtx.Error("Failed to check for duplicate", "error", err)
		return err
	}
	if isDuplicate {
		logCtx.Info("Duplicate file detected. Skipping.", "existingDocId", docID)
		return nil // Clean exit for a duplicate
	}

	docRef, err := f.createInitialDocument(ctx, fileHash, e.Name)
	if err != nil {
		logCtx.Error("Failed to create initial Firestore document", "error", err)
		return err
	}
	logCtx = logCtx.With("documentId", docRef.ID)
	logCtx.Info("Created master document in Firestore.")

	optimizedPdfPath := filepath.Join(tempDir, "optimized.pdf")
	pageCount, err := f.optimizeAndPrepare(ctx, logCtx, docRef, sourcePdfPath, optimizedPdfPath)
	if err != nil {
		// Error is already logged and handled in optimizeAndPrepare
		return err
	}

	if err := f.uploadSplitPages(ctx, logCtx, docRef, optimizedPdfPath, pageCount); err != nil {
		// Error is already logged and handled in uploadSplitPages
		return err
	}

	if err := f.triggerWorkflow(ctx, logCtx, docRef, pageCount); err != nil {
		// Error is already logged and handled in triggerWorkflow
		return err
	}

	logCtx.Info("Hand-off to workflow complete.")
	return nil
}

func (f *PDFSplitterFunction) isDuplicate(ctx context.Context, fileHash string) (bool, string, error) {
	docs, err := f.firestoreClient.Collection(f.config.CollectionName).Where("fileHash", "==", fileHash).Limit(1).Documents(ctx).GetAll()
	if err != nil {
		return false, "", fmt.Errorf("failed to query for duplicates: %w", err)
	}
	if len(docs) > 0 {
		return true, docs[0].Ref.ID, nil
	}
	return false, "", nil
}

func (f *PDFSplitterFunction) createInitialDocument(ctx context.Context, fileHash, filename string) (*firestore.DocumentRef, error) {
	newDoc := models.Document{
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

func (f *PDFSplitterFunction) optimizeAndPrepare(ctx context.Context, logCtx *slog.Logger, docRef *firestore.DocumentRef, source, optimized string) (int, error) {
	if err := optimizePDF(source, optimized); err != nil {
		return 0, f.handleError(ctx, logCtx, docRef, "failed to validate/optimize PDF", err)
	}
	pageCount, err := api.PageCountFile(optimized)
	if err != nil {
		return 0, f.handleError(ctx, logCtx, docRef, "failed to get page count", err)
	}
	if err := api.SplitFile(optimized, filepath.Dir(optimized), 1, nil); err != nil {
		return 0, f.handleError(ctx, logCtx, docRef, "failed to split PDF", err)
	}
	updates := []firestore.Update{
		{Path: "status", Value: "SPLITTING"},
		{Path: "pageCount", Value: pageCount},
	}
	if _, err := docRef.Update(ctx, updates); err != nil {
		return 0, f.handleError(ctx, logCtx, docRef, "failed to update status to SPLITTING", err)
	}
	logCtx.Info("PDF optimized and split locally.", "pageCount", pageCount)
	return pageCount, nil
}

func (f *PDFSplitterFunction) uploadSplitPages(ctx context.Context, logCtx *slog.Logger, docRef *firestore.DocumentRef, optimizedPdfPath string, pageCount int) error {
	logCtx.Info("Starting concurrent upload of pages.", "pageCount", pageCount)
	eg, gctx := errgroup.WithContext(ctx)
	eg.SetLimit(10)

	splitFileBase := strings.TrimSuffix(optimizedPdfPath, filepath.Ext(optimizedPdfPath))

	for i := 1; i <= pageCount; i++ {
		pageNumber := i
		localSplitFilePath := fmt.Sprintf("%s_%d.pdf", splitFileBase, pageNumber)
		gcsDestObject := fmt.Sprintf("%s/%05d.pdf", docRef.ID, pageNumber)

		eg.Go(func() error {
			if err := f.uploadFile(gctx, localSplitFilePath, gcsDestObject); err != nil {
				return fmt.Errorf("page %d: %w", pageNumber, err)
			}
			return nil
		})
	}
	if err := eg.Wait(); err != nil {
		return f.handleError(ctx, logCtx, docRef, "one or more pages failed to upload", err)
	}
	logCtx.Info("All pages uploaded successfully.")
	return nil
}

func (f *PDFSplitterFunction) triggerWorkflow(ctx context.Context, logCtx *slog.Logger, docRef *firestore.DocumentRef, pageCount int) error {
	logCtx.Info("Triggering workflow.")
	workflowPayload := map[string]interface{}{
		"documentId": docRef.ID,
		"pageCount":  pageCount,
	}
	payloadBytes, err := json.Marshal(workflowPayload)
	if err != nil {
		return f.handleError(ctx, logCtx, docRef, "failed to marshal workflow payload", err)
	}
	req := &executionspb.CreateExecutionRequest{
		Parent: fmt.Sprintf("projects/%s/locations/%s/workflows/%s", f.config.ProjectID, f.config.WorkflowLocation, f.config.WorkflowID),
		Execution: &executionspb.Execution{
			Argument: string(payloadBytes),
		},
	}
	_, err = f.executionsClient.CreateExecution(ctx, req)
	if err != nil {
		return f.handleError(ctx, logCtx, docRef, "failed to trigger workflow execution", err)
	}
	return nil
}

func (f *PDFSplitterFunction) handleError(ctx context.Context, logCtx *slog.Logger, docRef *firestore.DocumentRef, message string, originalErr error) error {
	fullError := fmt.Sprintf("%s: %v", message, originalErr)
	logCtx.Error(message, "error", originalErr)
	if err := f.updateStatus(ctx, docRef, "FAILED", fullError); err != nil {
		logCtx.Error("CRITICAL: Failed to update Firestore status to FAILED after a processing error.", "updateError", err)
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

func (f *PDFSplitterFunction) uploadFile(ctx context.Context, localPath, destObject string) error {
	const maxRetries = 4
	var backoff = 1 * time.Second
	var lastErr error

	for i := 0; i < maxRetries; i++ {
		err := func() error {
			localFileReader, err := os.Open(localPath)
			if err != nil {
				return fmt.Errorf("could not open local file %s: %w", localPath, err)
			}
			defer localFileReader.Close()

			writeCtx, cancel := context.WithTimeout(ctx, time.Second*50)
			defer cancel()

			gcsWriter := f.storageClient.Bucket(f.config.SplitPagesBucket).Object(destObject).NewWriter(writeCtx)

			if _, err := io.Copy(gcsWriter, localFileReader); err != nil {
				_ = gcsWriter.Close()
				return fmt.Errorf("io.Copy to GCS failed: %w", err)
			}

			if err := gcsWriter.Close(); err != nil {
				return fmt.Errorf("failed to close GCS writer (finalize upload): %w", err)
			}
			return nil
		}()

		if err == nil {
			return nil // Success!
		}

		lastErr = err
		slog.Warn(
			"Upload failed, will retry.",
			"gcsObject", destObject,
			"attempt", i+1,
			"maxRetries", maxRetries,
			"backoff", backoff.String(),
			"error", err,
		)

		select {
		case <-time.After(backoff):
			backoff *= 2
		case <-ctx.Done():
			slog.Error("Context cancelled during backoff. Aborting retries.", "gcsObject", destObject, "error", ctx.Err())
			return ctx.Err()
		}
	}
	slog.Error("Upload failed after all retries.", "gcsObject", destObject, "error", lastErr)
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
