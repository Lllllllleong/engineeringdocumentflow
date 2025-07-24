package services

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"cloud.google.com/go/storage"
	"cloud.google.com/go/vertexai/genai"
	"github.com/Lllllllleong/engineeringdocumentflow/internal/gcp"
	"github.com/Lllllllleong/engineeringdocumentflow/internal/models"
)

// TranslatorConfig holds all configuration for the translator service.
type TranslatorConfig struct {
	ProjectID      string
	VertexAIRegion string
	MarkdownBucket string
}

// TranslatorFunction holds the dependencies for the translation logic.
type TranslatorFunction struct {
	storageClient *storage.Client
	vertexClient  *gcp.VertexClient
	config        TranslatorConfig
}

// loadConfig loads and validates all necessary environment variables for this service.
func loadConfig() (*TranslatorConfig, error) {
	projectID := gcp.GetEnv("PROJECT_ID", "")
	if projectID == "" {
		return nil, fmt.Errorf("GCP_PROJECT environment variable must be set")
	}
	markdownBucket := gcp.GetEnv("TRANSLATED_MARKDOWN_BUCKET", "")
	if markdownBucket == "" {
		return nil, fmt.Errorf("TRANSLATED_MARKDOWN_BUCKET environment variable must be set")
	}

	return &TranslatorConfig{
		ProjectID:      projectID,
		VertexAIRegion: gcp.GetEnv("VERTEX_AI_REGION", "us-central1"),
		MarkdownBucket: markdownBucket,
	}, nil
}

// NewTranslator creates a new TranslatorFunction instance.
func NewTranslator(ctx context.Context) (*TranslatorFunction, error) {
	config, err := loadConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to load configuration: %w", err)
	}

	storageClient, err := storage.NewClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create storage client: %w", err)
	}

	vertexClient, err := gcp.NewVertexClient(ctx, config.ProjectID, config.VertexAIRegion)
	if err != nil {
		return nil, fmt.Errorf("failed to create vertex client: %w", err)
	}

	return &TranslatorFunction{
		storageClient: storageClient,
		vertexClient:  vertexClient,
		config:        *config,
	}, nil
}

// Process handles the core logic of translating a single PDF page to Markdown.
func (f *TranslatorFunction) Process(ctx context.Context, req *models.PageTranslatorRequest) (*models.PageTranslatorResponse, error) {
	logCtx := slog.With(
		"documentId", req.DocumentID,
		"pageNumber", req.PageNumber,
		"executionId", req.ExecutionID,
	)
	logCtx.Info("Starting translation.")

	model := f.vertexClient.TranslatorModel
	prompt := genai.Text(gcp.TranslatorUserPrompt)
	filePart := genai.FileData{
		MIMEType: "application/pdf",
		FileURI:  req.GCSUri,
	}

	geminiResp, err := model.GenerateContent(ctx, filePart, prompt)
	if err != nil {
		logCtx.Error("Call to Vertex AI failed", "error", err)
		return nil, fmt.Errorf("failed to generate content from gemini: %w", err)
	}

	markdownContent := f.extractMarkdown(geminiResp, req)

	// Sanity check for LLM refusal.
	refusalPhrases := []string{
		"i am unable to",
		"i cannot fulfill",
		"i cannot answer",
		"i cannot provide",
		"as a large language model",
	}
	lowerMarkdownContent := strings.ToLower(markdownContent)
	for _, phrase := range refusalPhrases {
		if strings.Contains(lowerMarkdownContent, phrase) {
			err := fmt.Errorf("gemini response indicates refusal for page %d", req.PageNumber)
			logCtx.Error("LLM refusal detected", "error", err, "response", markdownContent)
			return nil, err // This will fail the step in the workflow.
		}
	}

	if markdownContent == "" {
		logCtx.Warn("No markdown content extracted from response. Treating as empty page.")
	}

	// --- Use the shared, atomic GCS save function ---
	objectName := fmt.Sprintf("%s/%05d.md", req.DocumentID, req.PageNumber)
	bucketHandle := f.storageClient.Bucket(f.config.MarkdownBucket)

	if err := gcp.SaveToGCSAtomically(ctx, bucketHandle, objectName, markdownContent); err != nil {
		// The shared function logs the generic error, but we add our own with more context.
		logCtx.Error("Failed to save to GCS atomically", "error", err, "bucket", f.config.MarkdownBucket, "object", objectName)
		return nil, err
	}

	outputGCSUri := fmt.Sprintf("gs://%s/%s", f.config.MarkdownBucket, objectName)
	logCtx.Info("Translation complete.", "outputGcsUri", outputGCSUri)
	return &models.PageTranslatorResponse{
		Status:       "success",
		OutputGCSUri: outputGCSUri,
	}, nil
}

// extractMarkdown parses the model's response and robustly extracts text content.
func (f *TranslatorFunction) extractMarkdown(resp *genai.GenerateContentResponse, req *models.PageTranslatorRequest) string {
	if resp == nil || len(resp.Candidates) == 0 || resp.Candidates[0].Content == nil || len(resp.Candidates[0].Content.Parts) == 0 {
		return ""
	}

	var markdownContent strings.Builder
	var textPartsFound int
	for _, part := range resp.Candidates[0].Content.Parts {
		if txt, ok := part.(genai.Text); ok {
			markdownContent.WriteString(string(txt))
			textPartsFound++
		}
	}

	if textPartsFound > 1 {
		slog.Warn(
			"Gemini response contained multiple text parts; they have been concatenated.",
			"documentId", req.DocumentID,
			"pageNumber", req.PageNumber,
			"executionId", req.ExecutionID,
			"partCount", textPartsFound,
		)
	}

	contentStr := strings.TrimSpace(markdownContent.String())
	contentStr = strings.TrimPrefix(contentStr, "```markdown")
	contentStr = strings.TrimPrefix(contentStr, "```")
	contentStr = strings.TrimSuffix(contentStr, "```")
	return strings.TrimSpace(contentStr)
}
