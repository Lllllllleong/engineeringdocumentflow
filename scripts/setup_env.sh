#!/bin/bash
# =============================================================================
#           ENVIRONMENT VARIABLES FOR THE DOCUMENT PROCESSING PIPELINE
# =============================================================================
#
# INSTRUCTIONS:
# 1. This file is the single source of truth for environment configuration.
# 2. Run this script from the project root directory using the `source` command:
#
#    source ./scripts/setup_env.sh
#
# =============================================================================

# --- Core Project & Region Configuration ---
# Used by gcloud CLI and all Go services.
export PROJECT_ID="engineeringdocumentflow"
export REGION="us-central1"
export VERTEX_AI_REGION="us-central1" # Can be different from REGION if needed

# --- Service Account ---
# The single service account used by all functions and the workflow.
export SERVICE_ACCOUNT_EMAIL="pipeline-runner-sa@${PROJECT_ID}.iam.gserviceaccount.com"

# --- GCS Buckets (as per the revised plan) ---
# Each variable corresponds to a bucket used by one or more services.
export UPLOADS_BUCKET="${PROJECT_ID}-ingest"
export SPLIT_PAGES_BUCKET="${PROJECT_ID}-split-pages"
export TRANSLATED_MARKDOWN_BUCKET="${PROJECT_ID}-translated-markdown"
export AGGREGATED_MARKDOWN_BUCKET="${PROJECT_ID}-aggregated-markdown"
export CLEANED_MARKDOWN_BUCKET="${PROJECT_ID}-cleaned-markdown"
export FINAL_SECTIONS_BUCKET="${PROJECT_ID}-final-sections"

# --- Workflow & Firestore Configuration ---
export WORKFLOW_LOCATION="us-central1"
export WORKFLOW_ID="document-processing-orchestrator"
export FIRESTORE_COLLECTION="documents"

# --- Cloud Function URLs (REMOVED) ---
# These are now set dynamically by the ./scripts/deploy.sh script after
# each function is deployed. There is no need to define them here.

echo "✅ Environment variables set for the Document Processing Pipeline."

