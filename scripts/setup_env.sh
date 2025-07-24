#!/bin/bash
# =============================================================================
#           ENVIRONMENT VARIABLES FOR THE DOCUMENT PROCESSING PIPELINE
# =============================================================================
#
# INSTRUCTIONS:
# 1. Fill in the values below.
# 2. Run this script from the project root directory using the `source` command:
#
#    source ./scripts/setup_env.sh
#
# =============================================================================

# --- Scripting Variables (for deployment scripts) ---
export PROJECT_ID="engineeringdocumentflow"
export REGION="us-central1"
export TRIGGER_BUCKET="engineeringdocumentflow-ingest"
# The service account that the Cloud Function will run as.
export SERVICE_ACCOUNT_EMAIL="splitter-sa@${PROJECT_ID}.iam.gserviceaccount.com"

# --- Runtime Variables for '1-pdf-splitter' (read by main.go) ---
export SPLIT_PAGES_BUCKET="engineeringdocumentflow-split-pdfs"
export FIRESTORE_COLLECTION="documents"
export WORKFLOW_LOCATION="us-central1"
export WORKFLOW_ID="document-processing-orchestrator"

echo "✅ Environment variables set for the Document Processing Pipeline."