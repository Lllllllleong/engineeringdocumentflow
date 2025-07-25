package gcp

import (
	"cloud.google.com/go/vertexai/genai"
	"context"
	"fmt"
		_ "github.com/GoogleCloudPlatform/functions-framework-go/functions"
)

// --- Translator Model Prompts ---
const TranslatorSystemPrompt = "You are a document parser and markdown translator. Your task is to parse the content of a PDF document and translate it into markdown format. Accuracy, detail, and information preservation are of utmost importance."
const TranslatorUserPrompt = `You will be provided with a PDF document:

Follow these instructions to parse the document and translate its content into markdown format:

Text: Parse all text content directly into markdown text.
Lists: Parse all lists into markdown lists, maintaining the original structure and formatting.
Images: Replace each image with a descriptive text that accurately describes the image's content. Be as detailed as possible in your description.
Tables: Parse all tables into markdown tables. If a table contains merged cells, normalize the table by copying and appending the content from the parent cells into the normalized child cells. This ensures that as much information as possible is preserved.
Headers and Footers: Ignore any irrelevant content in the header and footer, such as the publishing company's name, logo, address, or page numbers. Focus on preserving the core content of the document.
Your primary goal is to maintain the integrity and completeness of the document's content in the markdown output. Ensure that all details and information are accurately translated and preserved.`

// --- Cleaner Model Prompts ---
const CleanerSystemPrompt = "You are an expert Markdown editor. Your task is to clean, refine, and consolidate a single Markdown file that was created by merging multiple pages. Your goal is to make it a single, cohesive, and perfectly formatted document."
const CleanerUserPrompt = `Follow these instructions to clean, refine, and consolidate the Markdown file:

1.  **Merge Broken Tables**: Identify table headers and content that are separated by page breaks or separators and merge them into a single, correctly formatted Markdown table.
2.  **Smooth Formatting**: Ensure consistent heading levels, list formatting, and code blocks. Remove awkward line breaks in the middle of sentences that were caused by page breaks.
3.  **Remove Artifacts**: Delete any repeated page numbers, company logo names/address, or page separators (e.g., a line of '---') that are not part of the content's structure.
4.  **Consolidate Sections**: Ensure a logical flow between sections that were previously on different pages. Do not add new content, but smooth the transition.

Attempt to preserve as much information as possible. Only remove sections if you are absolutely certain it is noise. If you are uncertain, just leave it in.

Return ONLY the final, cleaned Markdown content. Do not include any preambles like "Here is the cleaned markdown" or surround the output with backtick fences unless the content itself is a code block.`

// --- Section Splitter Model Prompts ---
const SectionSplitterSystemPrompt = "You are a specialist document analysis tool. Your task is to semantically split a large markdown document into sections based on its headers. You must output your response as a valid JSON array."
const SectionSplitterUserPrompt = `Analyze the provided markdown document. Your task is to split it into logical sections.

Follow these rules precisely:
1.  Identify the main sections of the document, typically marked by headers like '# Title', '## Subtitle', or numbered headers like '1. Introduction', '1.1 Background'.
2.  Create a JSON object for each section.
3.  Each JSON object must have exactly two keys:
    - "section": A string containing the full header title (e.g., "1.1.2 Background and Motivation").
    - "content": A string containing all the markdown content that belongs under that header, up to the next header of the same or higher level.
4.  The final output MUST be a single, valid JSON array of these objects. Do not include any text before or after the JSON array.

Example output format:
[
  {
    "section": "1. Introduction",
    "content": "This is the full text of the introduction..."
  },
  {
    "section": "1.1 Background",
    "content": "This is the content for the background section..."
  },
  {
    "section": "2. Main Body",
    "content": "Content for the main body goes here."
  }
]`

// VertexClient holds all pre-configured generative models for our app.
type VertexClient struct {
	TranslatorModel      *genai.GenerativeModel
	CleanerModel         *genai.GenerativeModel
	SectionSplitterModel *genai.GenerativeModel // <-- ADDED
	baseClient           *genai.Client
}

// NewVertexClient creates a new client holding all necessary models.
func NewVertexClient(ctx context.Context, projectID, region string) (*VertexClient, error) {
	if projectID == "" || region == "" {
		return nil, fmt.Errorf("NewVertexClient: projectID and region cannot be empty")
	}

	baseClient, err := genai.NewClient(ctx, projectID, region)
	if err != nil {
		return nil, fmt.Errorf("genai.NewClient: %w", err)
	}

	// --- Configure the translator model ---
	translatorModel := baseClient.GenerativeModel("gemini-1.5-pro")
	translatorModel.SystemInstruction = &genai.Content{
		Parts: []genai.Part{genai.Text(TranslatorSystemPrompt)},
	}
	// ... (translator model config remains the same) ...

	// --- Configure the cleaner model ---
	cleanerModel := baseClient.GenerativeModel("gemini-1.5-pro")
	cleanerModel.SystemInstruction = &genai.Content{
		Parts: []genai.Part{genai.Text(CleanerSystemPrompt)},
	}
	// ... (cleaner model config remains the same) ...

	// --- Configure the section splitter model ---
	sectionSplitterModel := baseClient.GenerativeModel("gemini-1.5-pro")
	sectionSplitterModel.SystemInstruction = &genai.Content{
		Parts: []genai.Part{genai.Text(SectionSplitterSystemPrompt)},
	}
	sectionSplitterModel.GenerationConfig = genai.GenerationConfig{
		// Force JSON output. This is a critical setting for this model.
		ResponseMIMEType: "application/json",
		Temperature:      genai.Ptr[float32](0.0), // Low temp for deterministic, structured output
	}
	sectionSplitterModel.SafetySettings = []*genai.SafetySetting{
		{Category: genai.HarmCategoryHateSpeech, Threshold: genai.HarmBlockNone},
		{Category: genai.HarmCategoryDangerousContent, Threshold: genai.HarmBlockNone},
		{Category: genai.HarmCategorySexuallyExplicit, Threshold: genai.HarmBlockNone},
		{Category: genai.HarmCategoryHarassment, Threshold: genai.HarmBlockNone},
	}

	return &VertexClient{
		TranslatorModel:      translatorModel,
		CleanerModel:         cleanerModel,
		SectionSplitterModel: sectionSplitterModel, // <-- ADDED
		baseClient:           baseClient,
	}, nil
}

func (c *VertexClient) Close() error {
	if c.baseClient != nil {
		return c.baseClient.Close()
	}
	return nil
}