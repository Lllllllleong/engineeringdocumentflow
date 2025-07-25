package services

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"regexp"
	"strings"

	"cloud.google.com/go/storage"
	"cloud.google.com/go/vertexai/genai"
	"github.com/Lllllllleong/engineeringdocumentflow/internal/gcp"
	"github.com/Lllllllleong/engineeringdocumentflow/internal/models"
		_ "github.com/GoogleCloudPlatform/functions-framework-go/functions"
)

// SectionSplitterConfig holds configuration for the section_splitter service.
type SectionSplitterConfig struct {
	ProjectID           string
	VertexAIRegion      string
	FinalSectionsBucket string
}

// SectionSplitterFunction holds dependencies for the section splitting logic.
type SectionSplitterFunction struct {
	storageClient *storage.Client
	vertexClient  *gcp.VertexClient
	config        SectionSplitterConfig
}

// parsedSection defines the structure of the JSON objects we expect from the Gemini response.
type parsedSection struct {
	Section string `json:"section"`
	Content string `json:"content"`
}

// NewSectionSplitter creates a new SectionSplitterFunction instance.
func NewSectionSplitter(ctx context.Context) (*SectionSplitterFunction, error) {
	projectID := gcp.GetEnv("GOOGLE_CLOUD_PROJECT_ID", "")
	if projectID == "" {
		return nil, fmt.Errorf("GOOGLE_CLOUD_PROJECT_ID environment variable must be set")
	}

	config := SectionSplitterConfig{
		ProjectID:           projectID,
		VertexAIRegion:      gcp.GetEnv("VERTEX_AI_REGION", "us-central1"),
		FinalSectionsBucket: gcp.GetEnv("FINAL_SECTIONS_BUCKET", ""),
	}
	if config.FinalSectionsBucket == "" {
		return nil, fmt.Errorf("FINAL_SECTIONS_BUCKET must be set")
	}

	storageClient, err := storage.NewClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create storage client: %w", err)
	}

	vertexClient, err := gcp.NewVertexClient(ctx, config.ProjectID, config.VertexAIRegion)
	if err != nil {
		return nil, fmt.Errorf("failed to create vertex client: %w", err)
	}

	return &SectionSplitterFunction{
		storageClient: storageClient,
		vertexClient:  vertexClient,
		config:        config,
	}, nil
}

// Process handles the core logic of splitting a markdown file into sections.
func (f *SectionSplitterFunction) Process(ctx context.Context, req *models.SectionSplitterRequest) (*models.SectionSplitterResponse, error) {
	logCtx := slog.With("documentId", req.DocumentID, "executionId", req.ExecutionID)
	logCtx.Info("Starting section splitting.", "gcsUri", req.CleanedGCSUri)

	// --- 1. Call the pre-configured section splitter model ---
	model := f.vertexClient.SectionSplitterModel
	prompt := genai.Text(gcp.SectionSplitterUserPrompt)
	filePart := genai.FileData{
		MIMEType: "text/markdown",
		FileURI:  req.CleanedGCSUri,
	}

	resp, err := model.GenerateContent(ctx, filePart, prompt)
	if err != nil {
		logCtx.Error("Call to Vertex AI for section splitting failed", "error", err)
		return nil, fmt.Errorf("failed to generate sections from gemini: %w", err)
	}

	// --- 2. Extract and parse the JSON response ---
	jsonString := f.extractJSONContent(resp)
	if jsonString == "" {
		err := fmt.Errorf("gemini returned an empty response instead of JSON for document ID %s", req.DocumentID)
		logCtx.Error("Empty response from Gemini", "error", err)
		return nil, err
	}

	var sections []parsedSection
	if err := json.Unmarshal([]byte(jsonString), &sections); err != nil {
		logCtx.Error("Failed to unmarshal JSON response from Gemini", "error", err, "responseBody", jsonString)
		return nil, fmt.Errorf("failed to parse JSON from model for document ID %s: %w", req.DocumentID, err)
	}

	if len(sections) == 0 {
		logCtx.Warn("Model returned a valid but empty JSON array. No sections to process.")
		return &models.SectionSplitterResponse{Status: "success", SectionCount: 0}, nil
	}

	// --- 3. Save each section to a separate file in GCS ---
	logCtx.Info("Successfully parsed sections. Saving to GCS...", "sectionCount", len(sections))
	bucketHandle := f.storageClient.Bucket(f.config.FinalSectionsBucket)
	var savedCount int

	for i, section := range sections {
		sanitizedTitle := f.sanitizeFileName(section.Section)
		if sanitizedTitle == "" {
			sanitizedTitle = fmt.Sprintf("untitled_section_%d", i+1)
		}

		objectName := fmt.Sprintf("%s/%s.md", req.DocumentID, sanitizedTitle)

		if err := gcp.SaveToGCSAtomically(ctx, bucketHandle, objectName, section.Content); err != nil {
			logCtx.Error("Failed to save section", "error", err, "sectionTitle", section.Section, "objectName", objectName)
			// We choose to continue processing other sections even if one fails.
		} else {
			savedCount++
		}
	}

	logCtx.Info("Section splitting complete.", "savedCount", savedCount, "totalSections", len(sections))

	return &models.SectionSplitterResponse{
		Status:       "success",
		SectionCount: savedCount,
	}, nil
}

// extractJSONContent robustly gets the raw text content from the model response.
func (f *SectionSplitterFunction) extractJSONContent(resp *genai.GenerateContentResponse) string {
	if resp == nil || len(resp.Candidates) == 0 || resp.Candidates[0].Content == nil || len(resp.Candidates[0].Content.Parts) == 0 {
		return ""
	}
	// The model is configured to return JSON, so we expect a single text part.
	if txt, ok := resp.Candidates[0].Content.Parts[0].(genai.Text); ok {
		// Clean potential markdown fences just in case
		cleanJSON := strings.TrimSpace(string(txt))
		cleanJSON = strings.TrimPrefix(cleanJSON, "```json")
		cleanJSON = strings.TrimSuffix(cleanJSON, "```")
		return strings.TrimSpace(cleanJSON)
	}
	return ""
}

// nonAlphanumericRegex is a compiled regex for efficiency.
var nonAlphanumericRegex = regexp.MustCompile(`[^a-z0-9]+`)

// sanitizeFileName converts a section title into a safe GCS object name component.
func (f *SectionSplitterFunction) sanitizeFileName(title string) string {
	// Convert to lowercase
	lower := strings.ToLower(title)
	// Replace any sequence of non-alphanumeric characters with a single underscore
	sanitized := nonAlphanumericRegex.ReplaceAllString(lower, "_")
	// Remove leading/trailing underscores
	sanitized = strings.Trim(sanitized, "_")

	// Truncate to a reasonable length to avoid overly long filenames
	const maxLength = 100
	if len(sanitized) > maxLength {
		sanitized = sanitized[:maxLength]
		// Trim again in case we cut on an underscore
		sanitized = strings.Trim(sanitized, "_")
	}

	return sanitized
}
