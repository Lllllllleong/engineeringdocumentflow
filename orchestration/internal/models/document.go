package models

import "time"

// Document represents the main record for a PDF file in Firestore.
// It is shared across multiple services in the pipeline.
type Document struct {
	FileHash         string    `firestore:"fileHash,omitempty"`
	OriginalFilename string    `firestore:"originalFilename,omitempty"`
	Status           string    `firestore:"status,omitempty"`
	ErrorDetails     string    `firestore:"errorDetails,omitempty"`
	PageCount        int       `firestore:"pageCount,omitempty"`
	CreatedAt        time.Time `firestore:"createdAt,omitempty"`
}
