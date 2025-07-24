package models

import "time"

// Document represents the main record for a PDF processing job in Firestore.
// It tracks the overall status and metadata of the file.
type Document struct {
	FileHash          string    `firestore:"fileHash,omitempty"`
	OriginalFilename  string    `firestore:"originalFilename,omitempty"`
	Status            string    `firestore:"status,omitempty"`
	ErrorDetails      string    `firestore:"errorDetails,omitempty"`
	PageCount         int       `firestore:"pageCount,omitempty"`
	WorkflowExecutionID string  `firestore:"workflowExecutionId,omitempty"` // For traceability
	CreatedAt         time.Time `firestore:"createdAt,omitempty"`
}
