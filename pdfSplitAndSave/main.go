package pdfSplitAndSave

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
	"sync"
	"time"

	"cloud.google.com/go/firestore"
	"cloud.google.com/go/storage"
	"github.com/GoogleCloudPlatform/functions-framework-go/functions"
	cloudevents "github.com/cloudevents/sdk-go/v2"
	"github.com/pdfcpu/pdfcpu/pkg/api"
	"golang.org/x/sync/errgroup"
	"google.golang.org/api/iterator"
)

// GCSEvent defines the structure for the GCS event data.
type GCSEvent struct {
	Bucket string `json:"bucket"`
	Name   string `json:"name"`
}

// config holds the configuration for the function.
var config struct {
	ProjectID        string
	SplitPagesBucket string
	CollectionName   string
}

var (
	storageClient   *storage.Client
	firestoreClient *firestore.Client
	initErr         error
	once            sync.Once
)

func init() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	// Initialize configuration and clients once.
	once.Do(func() {
		projectID := os.Getenv("PROJECT_ID")
		if projectID == "" {
			initErr = fmt.Errorf("PROJECT_ID environment variable must be set")
			return
		}
		config.ProjectID = projectID
		config.SplitPagesBucket = os.Getenv("SPLIT_PAGES_BUCKET")
		if config.SplitPagesBucket == "" {
			initErr = fmt.Errorf("SPLIT_PAGES_BUCKET environment variable must be set")
			return
		}
		config.CollectionName = os.Getenv("FIRESTORE_COLLECTION")
		if config.CollectionName == "" {
			initErr = fmt.Errorf("FIRESTORE_COLLECTION environment variable must be set")
			return
		}

		ctx := context.Background()
		storageClient, initErr = storage.NewClient(ctx)
		if initErr != nil {
			initErr = fmt.Errorf("failed to create storage client: %w", initErr)
			return
		}
		firestoreClient, initErr = firestore.NewClient(ctx, config.ProjectID)
		if initErr != nil {
			initErr = fmt.Errorf("failed to create firestore client: %w", initErr)
		}
	})

	// Register the CloudEvent function.
	// The first argument is the function name deployed in GCP.
	functions.CloudEvent("SplitAndPublish", SplitAndPublish)
}


// SplitAndPublish is the main function that processes the PDF.
func SplitAndPublish(ctx context.Context, e cloudevents.Event) error {
	if initErr != nil {
		slog.Error("Critical error during function initialization", "error", initErr)
		return initErr
	}

	var gcsEvent GCSEvent
	if err := json.Unmarshal(e.Data(), &gcsEvent); err != nil {
		slog.Error("Failed to unmarshal event data", "error", err, "data", string(e.Data()))
		return fmt.Errorf("json.Unmarshal: %w", err)
	}

	logCtx := slog.With("gcsBucket", gcsEvent.Bucket, "gcsObject", gcsEvent.Name)
	logCtx.Info("Processing new GCS object.")

	tempDir, err := os.MkdirTemp("", "pdf-splitter-*")
	if err != nil {
		return fmt.Errorf("failed to create temp dir: %w", err)
	}
	defer os.RemoveAll(tempDir)

	sourcePdfPath := filepath.Join(tempDir, "source.pdf")
	if err := streamGCSObject(ctx, gcsEvent.Bucket, gcsEvent.Name, sourcePdfPath); err != nil {
		logCtx.Error("Failed to download source PDF", "error", err)
		return err
	}

	fileHash, err := calculateFileHash(sourcePdfPath)
	if err != nil {
		logCtx.Error("Failed to calculate file hash", "error", err)
		return err
	}
	logCtx = logCtx.With("fileHash", fileHash)

	isDuplicate, docID, err := isDuplicate(ctx, fileHash)
	if err != nil {
		logCtx.Error("Failed to check for duplicate", "error", err)
		return err
	}
	if isDuplicate {
		logCtx.Info("Duplicate file detected. Skipping.", "existingDocId", docID)
		return nil
	}

	docRef, err := createInitialDocument(ctx, fileHash, gcsEvent.Name)
	if err != nil {
		logCtx.Error("Failed to create initial Firestore document", "error", err)
		return err
	}
	logCtx = logCtx.With("documentId", docRef.ID)
	logCtx.Info("Created master document in Firestore.")

	optimizedPdfPath := filepath.Join(tempDir, "optimized.pdf")
	pageCount, err := optimizeAndPrepare(ctx, logCtx, docRef, sourcePdfPath, optimizedPdfPath)
	if err != nil {
		return err
	}

	if err := uploadSplitPages(ctx, logCtx, docRef, optimizedPdfPath, pageCount); err != nil {
		return err
	}

	logCtx.Info("Successfully split and uploaded all pages.")
	return nil
}

func streamGCSObject(ctx context.Context, bucket, object, destPath string) error {
	r, err := storageClient.Bucket(bucket).Object(object).NewReader(ctx)
	if err != nil {
		return err
	}
	defer r.Close()

	f, err := os.Create(destPath)
	if err != nil {
		return err
	}
	defer f.Close()

	if _, err := io.Copy(f, r); err != nil {
		return err
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

func isDuplicate(ctx context.Context, fileHash string) (bool, string, error) {
	iter := firestoreClient.Collection(config.CollectionName).Where("fileHash", "==", fileHash).Limit(1).Documents(ctx)
	doc, err := iter.Next()
	if err == iterator.Done {
		return false, "", nil
	}
	if err != nil {
		return false, "", err
	}
	return true, doc.Ref.ID, nil
}

func createInitialDocument(ctx context.Context, fileHash, filename string) (*firestore.DocumentRef, error) {
	doc := map[string]interface{}{
		"fileHash":         fileHash,
		"originalFilename": filename,
		"status":           "PROCESSING",
		"createdAt":        time.Now(),
	}
	ref, _, err := firestoreClient.Collection(config.CollectionName).Add(ctx, doc)
	return ref, err
}

func optimizeAndPrepare(ctx context.Context, logCtx *slog.Logger, docRef *firestore.DocumentRef, source, optimized string) (int, error) {
	if err := api.OptimizeFile(source, optimized, nil); err != nil {
		return 0, handleError(ctx, logCtx, docRef, "failed to optimize PDF", err)
	}

	pageCount, err := api.PageCountFile(optimized)
	if err != nil {
		return 0, handleError(ctx, logCtx, docRef, "failed to get page count", err)
	}

	if err := api.SplitFile(optimized, filepath.Dir(optimized), 1, nil); err != nil {
		return 0, handleError(ctx, logCtx, docRef, "failed to split PDF", err)
	}

	if _, err := docRef.Update(ctx, []firestore.Update{
		{Path: "pageCount", Value: pageCount},
		{Path: "status", Value: "SPLITTING_COMPLETE"},
	}); err != nil {
		return 0, handleError(ctx, logCtx, docRef, "failed to update page count", err)
	}
	logCtx.Info("PDF optimized and split locally.", "pageCount", pageCount)
	return pageCount, nil
}

func uploadSplitPages(ctx context.Context, logCtx *slog.Logger, docRef *firestore.DocumentRef, optimizedPdfPath string, pageCount int) error {
	logCtx.Info("Starting concurrent upload of pages.", "pageCount", pageCount)
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(10) // Limit concurrency to avoid overwhelming the network or hitting API limits.

	splitFileBase := strings.TrimSuffix(optimizedPdfPath, filepath.Ext(optimizedPdfPath))

	for i := 1; i <= pageCount; i++ {
		pageNum := i
		g.Go(func() error {
			localPath := fmt.Sprintf("%s_%d.pdf", splitFileBase, pageNum)
			destObject := fmt.Sprintf("%s/%05d.pdf", docRef.ID, pageNum)

			if err := uploadFile(gctx, localPath, destObject); err != nil {
				return fmt.Errorf("failed to upload page %d: %w", pageNum, err)
			}
			return nil
		})
	}

	if err := g.Wait(); err != nil {
		return handleError(ctx, logCtx, docRef, "one or more pages failed to upload", err)
	}

	logCtx.Info("All pages uploaded successfully.")
	return nil
}

func uploadFile(ctx context.Context, localPath, destObject string) error {
	f, err := os.Open(localPath)
	if err != nil {
		return err
	}
	defer f.Close()

	w := storageClient.Bucket(config.SplitPagesBucket).Object(destObject).NewWriter(ctx)
	if _, err = io.Copy(w, f); err != nil {
		return err
	}
	return w.Close()
}

func handleError(ctx context.Context, logCtx *slog.Logger, docRef *firestore.DocumentRef, message string, originalErr error) error {
	logCtx.Error(message, "error", originalErr)
	if _, err := docRef.Update(ctx, []firestore.Update{
		{Path: "status", Value: "FAILED"},
		{Path: "errorDetails", Value: originalErr.Error()},
	}); err != nil {
		logCtx.Error("CRITICAL: Failed to update Firestore status to FAILED after a processing error.", "updateError", err)
	}
	return fmt.Errorf("%s: %w", message, originalErr)
}