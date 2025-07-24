package models

// These structs define the JSON payloads for HTTP requests and responses
// between the Cloud Workflow and the worker Cloud Functions.

// PageTranslatorRequest is the input for the page-translator function.
type PageTranslatorRequest struct {
	DocumentID  string `json:"documentId"`
	PageNumber  int    `json:"pageNumber"`
	GCSUri      string `json:"gcsUri"`
	ExecutionID string `json:"executionId"`
}

// PageTranslatorResponse is the output of the page-translator function.
type PageTranslatorResponse struct {
	Status       string `json:"status"`
	OutputGCSUri string `json:"outputGcsUri"`
}

// MarkdownAggregatorRequest is the input for the markdown-aggregator function.
type MarkdownAggregatorRequest struct {
	DocumentID  string `json:"documentId"`
	ExecutionID string `json:"executionId"`
}

// MarkdownAggregatorResponse is the output of the markdown-aggregator function.
type MarkdownAggregatorResponse struct {
	Status       string `json:"status"`
	MasterGCSUri string `json:"masterGcsUri"`
}

// MarkdownCleanerRequest is the input for the markdown-cleaner function.
type MarkdownCleanerRequest struct {
	DocumentID   string `json:"documentId"`
	MasterGCSUri string `json:"masterGcsUri"`
	ExecutionID  string `json:"executionId"`
}

// MarkdownCleanerResponse is the output of the markdown-cleaner function.
type MarkdownCleanerResponse struct {
	Status        string `json:"status"`
	CleanedGCSUri string `json:"cleanedGcsUri"`
}


type SectionSplitterRequest struct {
	DocumentID    string `json:"documentId"`
	CleanedGCSUri string `json:"cleanedGcsUri"`
	ExecutionID   string `json:"executionId"`
}


type SectionSplitterResponse struct {
	Status       string `json:"status"`
	SectionCount int    `json:"sectionCount"`
}