#!/bin/bash
# =============================================================================
#         Deployment Script for the Document Processing Pipeline
# =============================================================================
#
# USAGE:
# 1. First, run `source ./scripts/setup_env.sh` to load project variables.
# 2. Then, run this script directly: `./scripts/deploy.sh`
#
# =============================================================================

# Exit immediately if a command exits with a non-zero status.
set -e

# --- The base module path from your go.mod file ---
MODULE_PATH="github.com/Lllllllleong/engineeringdocumentflow"


# --- 1. Deploy the PDF Splitter Function (CloudEvent Trigger) ---
echo "--> Deploying pdf-splitter..."
gcloud functions deploy SplitAndPublish \
  --gen2 \
  --runtime=go121 \
  --region="${REGION}" \
  --source=. \
  --entry-point=SplitAndPublish \
  --trigger-event-filters="type=google.cloud.storage.object.v1.finalized" \
  --trigger-event-filters="bucket=${UPLOADS_BUCKET}" \
  --service-account="${SERVICE_ACCOUNT_EMAIL}" \
  --set-build-env-vars="GOOGLE_GOLANG_TARGET_PACKAGE=${MODULE_PATH}/cmd/pdf-splitter"

# --- 2. Deploy the Page Translator Function (HTTP Trigger) ---
echo "--> Deploying page-translator..."
gcloud functions deploy HandleTranslatePage \
  --gen2 \
  --runtime=go121 \
  --region="${REGION}" \
  --source=. \
  --entry-point=handleTranslatePage \
  --trigger-http \
  --no-allow-unauthenticated \
  --service-account="${SERVICE_ACCOUNT_EMAIL}" \
  --set-build-env-vars="GOOGLE_GOLANG_TARGET_PACKAGE=${MODULE_PATH}/cmd/page-translator"

# --- 3. Deploy the Markdown Aggregator Function (HTTP Trigger) ---
echo "--> Deploying markdown-aggregator..."
gcloud functions deploy HandleAggregateMarkdown \
  --gen2 \
  --runtime=go121 \
  --region="${REGION}" \
  --source=. \
  --entry-point=handleAggregateMarkdown \
  --trigger-http \
  --no-allow-unauthenticated \
  --service-account="${SERVICE_ACCOUNT_EMAIL}" \
  --set-build-env-vars="GOOGLE_GOLANG_TARGET_PACKAGE=${MODULE_PATH}/cmd/markdown-aggregator"

# --- 4. Deploy the Markdown Cleaner Function (HTTP Trigger) ---
echo "--> Deploying markdown-cleaner..."
gcloud functions deploy HandleCleanMarkdown \
  --gen2 \
  --runtime=go121 \
  --region="${REGION}" \
  --source=. \
  --entry-point=handleCleanMarkdown \
  --trigger-http \
  --no-allow-unauthenticated \
  --service-account="${SERVICE_ACCOUNT_EMAIL}" \
  --set-build-env-vars="GOOGLE_GOLANG_TARGET_PACKAGE=${MODULE_PATH}/cmd/markdown-cleaner"

# --- 5. Deploy the Section Splitter Function (HTTP Trigger) ---
echo "--> Deploying section-splitter..."
gcloud functions deploy HandleSplitSections \
  --gen2 \
  --runtime=go121 \
  --region="${REGION}" \
  --source=. \
  --entry-point=handleSplitSections \
  --trigger-http \
  --no-allow-unauthenticated \
  --service-account="${SERVICE_ACCOUNT_EMAIL}" \
  --set-build-env-vars="GOOGLE_GOLANG_TARGET_PACKAGE=${MODULE_PATH}/cmd/section-splitter"

# --- 6. Deploy the Main Orchestrator Workflow ---
echo "--> Deploying document-processing-orchestrator workflow..."
gcloud workflows deploy document-processing-orchestrator \
  --source=./orchestration/document-processing-orchestrator.yaml \
  --location="${WORKFLOW_LOCATION}" \
  --service-account="${SERVICE_ACCOUNT_EMAIL}"

# --- 7. Set PDF_SPLITTER_WORKER_URL environment variable ---
echo "--> Setting PDF_SPLITTER_WORKER_URL environment variable for the workflow..."
PDF_SPLITTER_URL=$(gcloud functions describe SplitAndPublish --region "${REGION}" --format 'value(serviceConfig.uri)')
gcloud workflows deploy document-processing-orchestrator \
    --source=./orchestration/document-processing-orchestrator.yaml \
    --location="${WORKFLOW_LOCATION}" \
    --service-account="${SERVICE_ACCOUNT_EMAIL}" \
    --set-env-vars="PDF_SPLITTER_WORKER_URL=${PDF_SPLITTER_URL}"

echo "✅ Deployment complete."