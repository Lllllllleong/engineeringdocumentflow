package services

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"strings"

	"cloud.google.com/go/aiplatform/apiv1/genai"
	"cloud.google.com/go/storage"
	"github.comcom/Lllllllleong/engineeringdocumentflow/internal/models"
)

// TranslatorConfig holds configuration settings for the translator service.
type TranslatorConfig struct {
	ProjectID      string
	VertexAIRegion string
	GeminiModel    string
	MarkdownBucket string
}

// TranslatorFunction holds dependencies for the translation logic.
type TranslatorFunction struct {
	storageClient  *storage.Client
	vertexAIClient *genai.Client // The long-lived client
	config         TranslatorConfig
}

// NewTranslator creates a new TranslatorFunction instance.
func NewTranslator(ctx context.Context) (*TranslatorFunction, error) {
	projectID := getEnv("GCP_PROJECT", "")
	if projectID == "" {
		return nil, fmt.Errorf("GCP_PROJECT environment variable must be set")
	}

	config := TranslatorConfig{
		ProjectID:      projectID,
		VertexAIRegion: getEnv("VERTEX_AI_REGION", "us-central1"),
		GeminiModel:    getEnv("GEMINI_MODEL_NAME", "gemini-2.5-pro"),
		MarkdownBucket: getEnv("MARKDOWN_BUCKET", ""),
	}
	if config.MarkdownBucket == "" {
		return nil, fmt.Errorf("MARKDOWN_BUCKET environment variable must be set")
	}

	storageClient, err := storage.NewClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create storage client: %w", err)
	}

	vertexAIClient, err := genai.NewClient(ctx, config.ProjectID, config.VertexAIRegion)
	if err != nil {
		return nil, fmt.Errorf("failed to create Vertex AI genai client: %w", err)
	}

	return &TranslatorFunction{
		storageClient:  storageClient,
		vertexAIClient: vertexAIClient,
		config:         config,
	}, nil
}

// Process handles the core logic of translating a single PDF page.
func (f *TranslatorFunction) Process(ctx context.Context, req *models.PageTranslatorRequest) (*models.PageTranslatorResponse, error) {
	log.Printf("[Doc: %s][Page: %d][Exec: %s] Starting translation for GCS URI: %s", req.DocumentID, req.PageNumber, req.ExecutionID, req.GCSUri)

	outputObjectName := fmt.Sprintf("%s/%d.md", req.DocumentID, req.PageNumber)

	// --- IDEMPOTENCY CHECK ---
	_, err := f.storageClient.Bucket(f.config.MarkdownBucket).Object(outputObjectName).Attrs(ctx)
	if err == nil {
		log.Printf("[Doc: %s][Page: %d][Exec: %s] Output already exists, skipping processing.", req.DocumentID, req.PageNumber, req.ExecutionID)
		outputGCSUri := fmt.Sprintf("gs://%s/%s", f.config.MarkdownBucket, outputObjectName)
		return &models.PageTranslatorResponse{Status: "success_skipped", OutputGCSUri: outputGCSUri}, nil
	}
	if err != storage.ErrObjectNotExist {
		log.Printf("[Doc: %s][Page: %d][Exec: %s] ERROR checking for existing object: %v", req.DocumentID, req.PageNumber, req.ExecutionID, err)
		return nil, err
	}
	// --- END IDEMPOTENCY CHECK ---

	// --- MODEL AND CONFIGURATION SETUP (from Python script) ---

	// 1. Get a generative model client.
	model := f.vertexAIClient.GenerativeModel(f.config.GeminiModel)

	// 2. Set the System Instruction.
	model.SystemInstruction = &genai.Content{
		Parts: []genai.Part{genai.Text("You are a document parser and markdown translator. Your task is to parse the content of a PDF document and translate it into markdown format. Accuracy, detail, and information preservation are of utmost importance.")},
	}

	// 3. Set the Generation Configuration.
	model.GenerationConfig = genai.GenerationConfig{
		Temperature:     genai.Ptr[float32](1.0),
		TopP:            genai.Ptr[float32](0.95),
		MaxOutputTokens: genai.Ptr[int32](65535),
		ThinkingConfig: &genai.ThinkingConfig{
			ThinkingBudget: genai.Ptr[int32](32768),
		},
	}

	// 4. Set the Safety Settings to OFF.
	model.SafetySettings = []*genai.SafetySetting{
		{Category: genai.HarmCategoryHateSpeech, Threshold: genai.HarmBlockNone},
		{Category: genai.HarmCategoryDangerousContent, Threshold: genai.HarmBlockNone},
		{Category: genai.HarmCategorySexuallyExplicit, Threshold: genai.HarmBlockNone},
		{Category: genai.HarmCategoryHarassment, Threshold: genai.HarmBlockNone},
	}

	// --- PROMPT AND FILE DATA ---

	// 5. Define the detailed user prompt.
	userPrompt := genai.Text(`You will be provided with a PDF document:

Follow these instructions to parse the document and translate its content into markdown format:

1. **Text:** Parse all text content directly into markdown text.
2. **Lists:** Parse all lists into markdown lists, maintaining the original structure and formatting.
3. **Images:** Replace each image with a descriptive text that accurately describes the image's content. Be as detailed as possible in your description.
4. **Tables:** Parse all tables into markdown tables. If a table contains merged cells, normalize the table by copying and appending the content from the parent cells into the normalized child cells. This ensures that as much information as possible is preserved.
5. **Headers and Footers:** Ignore any irrelevant content in the header and footer, such as the publishing company's name, logo, address, or page numbers. Focus on preserving the core content of the document.

Your primary goal is to maintain the integrity and completeness of the document's content in the markdown output. Ensure that all details and information are accurately translated and preserved.`)

	// 6. Define the file part using the GCS URI.
	filePart := genai.FileData{MIMEType: "application/pdf", FileURI: req.GCSUri}

	// --- API CALL ---
	log.Printf("[Doc: %s][Page: %d][Exec: %s] Calling Gemini %s API...", req.DocumentID, req.PageNumber, req.ExecutionID, f.config.GeminiModel)
	
    // We pass the parts to GenerateContent. The model object already holds all the configuration.
	resp, err := model.GenerateContent(ctx, filePart, userPrompt)
	if err != nil {
		log.Printf("[Doc: %s][Page: %d][Exec: %s] ERROR calling Gemini: %v", req.DocumentID, req.PageNumber, req.ExecutionID, err)
		return nil, fmt.Errorf("gemini API call failed: %w", err)
	}

	markdownContent, err := f.extractMarkdown(resp)
	if err != nil {
		log.Printf("[Doc: %s][Page: %d][Exec: %s] ERROR %v", req.DocumentID, req.PageNumber, req.ExecutionID, err)
		return nil, err
	}

	if err := f.saveToGCS(ctx, outputObjectName, markdownContent); err != nil {
		log.Printf("[Doc: %s][Page: %d][Exec: %s] ERROR saving markdown to GCS: %v", req.DocumentID, req.PageNumber, req.ExecutionID, err)
		return nil, err
	}
	log.Printf("[Doc: %s][Page: %d][Exec: %s] Successfully saved markdown to GCS.", req.DocumentID, req.PageNumber, req.ExecutionID)

	outputGCSUri := fmt.Sprintf("gs://%s/%s", f.config.MarkdownBucket, outputObjectName)
	return &models.PageTranslatorResponse{
		Status:       "success",
		OutputGCSUri: outputGCSUri,
	}, nil
}


// --- HELPER FUNCTIONS (No changes needed) ---

func (f *TranslatorFunction) extractMarkdown(resp *genai.GenerateContentResponse) (string, error) {
	if resp == nil || len(resp.Candidates) == 0 || resp.Candidates[0].Content == nil || len(resp.Candidates[0].Content.Parts) == 0 {
		if resp != nil && resp.PromptFeedback != nil && resp.PromptFeedback.BlockReason != genai.BlockedReasonUnspecified {
			return "", fmt.Errorf("gemini response blocked, reason: %s", resp.PromptFeedback.BlockReason.String())
		}
		return "", fmt.Errorf("invalid or empty response from Gemini")
	}
	if txt, ok := resp.Candidates[0].Content.Parts[0].(genai.Text); ok {
		content := string(txt)
		content = strings.TrimPrefix(content, "```markdown")
		content = strings.TrimPrefix(content, "```")
		content = strings.TrimSuffix(content, "```")
		content = strings.TrimSpace(content)
		if content == "" {
			return "", fmt.Errorf("extracted markdown content is empty")
		}
		return content, nil
	}
	return "", fmt.Errorf("gemini response did not contain a text part")
}

func (f *TranslatorFunction) saveToGCS(ctx context.Context, objectName, content string) error {
	writer := f.storageClient.Bucket(f.config.MarkdownBucket).Object(objectName).NewWriter(ctx)
	defer writer.Close()
	if _, err := io.Copy(writer, bytes.NewBufferString(content)); err != nil {
		return fmt.Errorf("io.Copy failed: %w", err)
	}
	return writer.Close()
}