package services

import (
	"context"
	"fmt"
	"log"
	"strings"

	"cloud.google.com/go/storage"
	"cloud.google.com/go/vertexai/genai"
	"github.com/Lllllllleong/engineeringdocumentflow/internal/gcp"
	"github.com/Lllllllleong/engineeringdocumentflow/internal/models"
)

// CleanerConfig holds configuration for the markdown-cleaner service.
type CleanerConfig struct {
	ProjectID          string
	VertexAIRegion     string
	CleanedMarkdownBucket string
}

// CleanerFunction holds dependencies for the cleaning logic.
type CleanerFunction struct {
	storageClient *storage.Client
	vertexClient  *gcp.VertexClient
	config        CleanerConfig
}

// NewCleaner creates a new CleanerFunction instance.
func NewCleaner(ctx context.Context) (*CleanerFunction, error) {
	projectID := gcp.GetEnv("PROJECT_ID", "")
	if projectID == "" {
		return nil, fmt.Errorf("GCP_PROJECT environment variable must be set")
	}

	config := CleanerConfig{
		ProjectID:          projectID,
		VertexAIRegion:     gcp.GetEnv("VERTEX_AI_REGION", "us-central1"),
		CleanedMarkdownBucket: gcp.GetEnv("CLEANED_MARKDOWN_BUCKET", ""), // Destination bucket
	}
	if config.CleanedMarkdownBucket == "" {
		return nil, fmt.Errorf("CLEANED_MARKDOWN_BUCKET must be set")
	}

	storageClient, err := storage.NewClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create storage client: %w", err)
	}

	// Re-use the centralized Vertex AI client constructor
	vertexClient, err := gcp.NewVertexClient(ctx, config.ProjectID, config.VertexAIRegion)
	if err != nil {
		return nil, fmt.Errorf("failed to create vertex client: %w", err)
	}

	return &CleanerFunction{
		storageClient: storageClient,
		vertexClient:  vertexClient,
		config:        config,
	}, nil
}

// Process handles the core logic of cleaning the aggregated Markdown file.
func (f *CleanerFunction) Process(ctx context.Context, req *models.MarkdownCleanerRequest) (*models.MarkdownCleanerResponse, error) {
	log.Printf("[Doc: %s][Exec: %s] Starting markdown cleanup.", req.DocumentID, req.ExecutionID)

	// --- 1. Call the pre-configured cleaner model ---
	// We pass the GCS URI directly, which is more efficient than reading the file into memory first.
	// The model has a large context window and is specifically prompted for this task.
	model := f.vertexClient.CleanerModel
	prompt := genai.Text(gcp.CleanerUserPrompt) // The user prompt is defined centrally
	filePart := genai.FileData{
		MIMEType: "text/markdown", // Inform the model about the content type
		FileURI:  req.MasterGCSUri,
	}

	geminiResp, err := model.GenerateContent(ctx, filePart, prompt)
	if err != nil {
		log.Printf("[Doc: %s][Exec: %s] ERROR calling Vertex AI for cleanup: %v", req.DocumentID, req.ExecutionID, err)
		return nil, fmt.Errorf("failed to generate cleaned content from gemini: %w", err)
	}

	// --- 2. Extract and validate the response ---
	cleanedContent := f.extractCleanedMarkdown(geminiResp)

	// Sanity check for LLM refusal.
	refusalPhrases := []string{
		"i am unable to",
		"i cannot fulfill",
		"i cannot answer",
		"as a large language model",
	}
	lowerCleanedContent := strings.ToLower(cleanedContent)
	for _, phrase := range refusalPhrases {
		if strings.Contains(lowerCleanedContent, phrase) {
			err := fmt.Errorf("gemini response indicates refusal to clean document")
			log.Printf("[Doc: %s][Exec: %s] ERROR: %v. Response: '%s'", req.DocumentID, req.ExecutionID, err, cleanedContent)
			return nil, err
		}
	}

	if cleanedContent == "" {
		log.Printf("[Doc: %s][Exec: %s] WARNING: No markdown content extracted from cleanup response. Saving empty file.", req.DocumentID, req.ExecutionID)
	}

	// --- 3. Save the cleaned content to the destination bucket ---
	objectName := fmt.Sprintf("%s/master.md", req.DocumentID)
	bucketHandle := f.storageClient.Bucket(f.config.CleanedMarkdownBucket)

	if err := gcp.SaveToGCSAtomically(ctx, bucketHandle, objectName, cleanedContent); err != nil {
		log.Printf("[Doc: %s][Exec: %s] ERROR: Failed to save cleaned markdown to GCS: %v", req.DocumentID, req.ExecutionID, err)
		return nil, err
	}

	// --- 4. Return the success response with the new URI ---
	outputGCSUri := fmt.Sprintf("gs://%s/%s", f.config.CleanedMarkdownBucket, objectName)
	log.Printf("[Doc: %s][Exec: %s] Markdown cleanup complete. Saved to %s", req.DocumentID, req.ExecutionID, outputGCSUri)

	return &models.MarkdownCleanerResponse{
		Status:        "success",
		CleanedGCSUri: outputGCSUri,
	}, nil
}

// extractCleanedMarkdown robustly parses the model's response to get the text content.
// It's similar to the translator's extractor but tailored for this service.
func (f *CleanerFunction) extractCleanedMarkdown(resp *genai.GenerateContentResponse) string {
	if resp == nil || len(resp.Candidates) == 0 || resp.Candidates[0].Content == nil || len(resp.Candidates[0].Content.Parts) == 0 {
		return ""
	}

	var contentBuilder strings.Builder
	for _, part := range resp.Candidates[0].Content.Parts {
		if txt, ok := part.(genai.Text); ok {
			contentBuilder.WriteString(string(txt))
		}
	}

	// The prompt instructs the model not to use markdown fences, but we clean them
	// just in case it disobeys.
	contentStr := strings.TrimSpace(contentBuilder.String())
	contentStr = strings.TrimPrefix(contentStr, "```markdown")
	contentStr = strings.TrimPrefix(contentStr, "```")
	contentStr = strings.TrimSuffix(contentStr, "```")
	return strings.TrimSpace(contentStr)
}