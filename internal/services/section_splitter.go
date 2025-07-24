package services

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"regexp"
	"strings"

	"cloud.google.com/go/storage"
	"cloud.google.com/go/vertexai/genai"
	"github.com/Lllllllleong/engineeringdocumentflow/internal/gcp"
	"github.com/Lllllllleong/engineeringdocumentflow/internal/models"
)

// SectionSplitterConfig holds configuration for the section_splitter service.
type SectionSplitterConfig struct {
	ProjectID         string
	VertexAIRegion    string
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
		ProjectID:         projectID,
		VertexAIRegion:    gcp.GetEnv("VERTEX_AI_REGION", "us-central1"),
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
	log.Printf("[Doc: %s][Exec: %s] Starting section splitting from URI: %s", req.DocumentID, req.ExecutionID, req.CleanedGCSUri)

	// --- 1. Call the pre-configured section splitter model ---
	model := f.vertexClient.SectionSplitterModel
	prompt := genai.Text(gcp.SectionSplitterUserPrompt)
	filePart := genai.FileData{
		MIMEType: "text/markdown",
		FileURI:  req.CleanedGCSUri,
	}

	resp, err := model.GenerateContent(ctx, filePart, prompt)
	if err != nil {
		log.Printf("[Doc: %s][Exec: %s] ERROR calling Vertex AI for section splitting: %v", req.DocumentID, req.ExecutionID, err)
		return nil, fmt.Errorf("failed to generate sections from gemini: %w", err)
	}

	// --- 2. Extract and parse the JSON response ---
	jsonString := f.extractJSONContent(resp)
	if jsonString == "" {
		log.Printf("[Doc: %s][Exec: %s] ERROR: Gemini returned an empty response instead of JSON.", req.DocumentID, req.ExecutionID)
		return nil, fmt.Errorf("gemini returned empty response for document ID %s", req.DocumentID)
	}

	var sections []parsedSection
	if err := json.Unmarshal([]byte(jsonString), &sections); err != nil {
		log.Printf("[Doc: %s][Exec: %s] ERROR: Failed to unmarshal JSON response from Gemini: %v. Response was: %s", req.DocumentID, req.ExecutionID, err, jsonString)
		return nil, fmt.Errorf("failed to parse JSON from model for document ID %s: %w", req.DocumentID, err)
	}

	if len(sections) == 0 {
		log.Printf("[Doc: %s][Exec: %s] WARNING: Model returned a valid but empty JSON array. No sections to process.", req.DocumentID, req.ExecutionID)
		return &models.SectionSplitterResponse{Status: "success", SectionCount: 0}, nil
	}

	// --- 3. Save each section to a separate file in GCS ---
	log.Printf("[Doc: %s][Exec: %s] Successfully parsed %d sections. Saving to GCS...", req.DocumentID, req.ExecutionID, len(sections))
	bucketHandle := f.storageClient.Bucket(f.config.FinalSectionsBucket)
	var savedCount int

	for i, section := range sections {
		// Sanitize the section title to create a safe and unique filename.
		sanitizedTitle := f.sanitizeFileName(section.Section)
		if sanitizedTitle == "" {
			sanitizedTitle = fmt.Sprintf("untitled_section_%d", i+1)
		}
		
		objectName := fmt.Sprintf("%s/%s.md", req.DocumentID, sanitizedTitle)

		if err := gcp.SaveToGCSAtomically(ctx, bucketHandle, objectName, section.Content); err != nil {
			log.Printf("[Doc: %s][Exec: %s] ERROR: Failed to save section '%s' to %s: %v. Continuing...", req.DocumentID, req.ExecutionID, section.Section, objectName, err)
			// We choose to continue processing other sections even if one fails.
		} else {
			savedCount++
		}
	}

	log.Printf("[Doc: %s][Exec: %s] Section splitting complete. Saved %d out of %d sections.", req.DocumentID, req.ExecutionID, savedCount, len(sections))

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
	if txt, ok := resp.Candidates[0].Content.Parts[0].(genai.Text); ok {
		return string(txt)
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
	if len(sanitized) > 100 {
		sanitized = sanitized[:100]
	}

	return sanitized
}