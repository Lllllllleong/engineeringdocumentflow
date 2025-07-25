#!/bin/bash
# =============================================================================
#         CORRECTED Deployment Script for the Document Processing Pipeline
# =============================================================================
# This script implements the per-function vendoring strategy to ensure
# reliable deployments from a Go monorepo.
# =============================================================================

# Exit immediately if a command exits with a non-zero status.
set -e

# --- Load Environment Configuration ---
source ./scripts/setup_env.sh
echo "✅ Environment variables loaded."

# --- Cleanup temporary build directory on script exit ---
trap 'echo "--- Cleaning up temporary build directory ---"; rm -rf /tmp/build' EXIT

# --- Define all functions to be deployed ---
FUNCTIONS=(
  "pdf-splitter"
  "page-translator"
  "markdown-aggregator"
  "markdown-cleaner"
  "section-splitter"
)

# --- Define the project's Go module path from go.mod ---
# This is the key to solving the error.
MODULE_PATH="github.com/Lllllllleong/engineeringdocumentflow"

echo ">>> Starting deployment for project: ${PROJECT_ID} in region: ${REGION}"

# --- Loop, Package, and Deploy Each Function ---
for FUNCTION_NAME in "${FUNCTIONS[@]}"; do
  BUILD_DIR="/tmp/build/${FUNCTION_NAME}"

  echo "-----------------------------------------------------"
  echo ">>> Preparing build directory for ${FUNCTION_NAME}"
  echo "-----------------------------------------------------"

  rm -rf "${BUILD_DIR}"
  mkdir -p "${BUILD_DIR}"

  # 1. AGGREGATE: Copy the function's specific code and all shared internal code.
  cp -r "cmd/${FUNCTION_NAME}/." "${BUILD_DIR}/"
  cp -r internal "${BUILD_DIR}/"

  echo "--- Vendoring dependencies for ${FUNCTION_NAME} ---"
  # 2. VENDOR: Create a fresh, self-contained Go module using the project's
  # canonical module path. This is the corrected sequence.
  (
    cd "${BUILD_DIR}"
    # Initialize a new go.mod file with a valid module path.
    go mod init "${MODULE_PATH}"
    # Tidy resolves all dependencies for the copied code.
    go mod tidy
    # Vendor creates the self-contained vendor/ directory.
    go mod vendor
  )

  echo "--- Deploying function: ${FUNCTION_NAME} ---"
  # 3. DEPLOY: Point the --source flag to the prepared, hermetic build directory.
  case ${FUNCTION_NAME} in
    "pdf-splitter")
      gcloud functions deploy SplitAndPublish \
        --gen2 \
        --runtime=go123 \
        --region="${REGION}" \
        --source="${BUILD_DIR}" \
        --entry-point=SplitAndPublish \
        --trigger-event-filters="type=google.cloud.storage.object.v1.finalized" \
        --trigger-event-filters="bucket=${UPLOADS_BUCKET}" \
        --service-account="${SERVICE_ACCOUNT_EMAIL}" \
        --quiet
      ;;
    "page-translator")
      gcloud functions deploy HandleTranslatePage \
        --gen2 \
        --runtime=go123 \
        --region="${REGION}" \
        --source="${BUILD_DIR}" \
        --entry-point=handleTranslatePage \
        --trigger-http \
        --no-allow-unauthenticated \
        --service-account="${SERVICE_ACCOUNT_EMAIL}" \
        --quiet
      ;;
    "markdown-aggregator")
      gcloud functions deploy HandleAggregateMarkdown \
        --gen2 \
        --runtime=go123 \
        --region="${REGION}" \
        --source="${BUILD_DIR}" \
        --entry-point=handleAggregateMarkdown \
        --trigger-http \
        --no-allow-unauthenticated \
        --service-account="${SERVICE_ACCOUNT_EMAIL}" \
        --quiet
      ;;
    "markdown-cleaner")
      gcloud functions deploy HandleCleanMarkdown \
        --gen2 \
        --runtime=go123 \
        --region="${REGION}" \
        --source="${BUILD_DIR}" \
        --entry-point=handleCleanMarkdown \
        --trigger-http \
        --no-allow-unauthenticated \
        --service-account="${SERVICE_ACCOUNT_EMAIL}" \
        --quiet
      ;;
    "section-splitter")
      gcloud functions deploy HandleSplitSections \
        --gen2 \
        --runtime=go123 \
        --region="${REGION}" \
        --source="${BUILD_DIR}" \
        --entry-point=handleSplitSections \
        --trigger-http \
        --no-allow-unauthenticated \
        --service-account="${SERVICE_ACCOUNT_EMAIL}" \
        --quiet
      ;;
  esac
done

# --- Deploy the Orchestrator Workflow ---
echo "-----------------------------------------------------"
echo ">>> Deploying Cloud Workflow"
echo "-----------------------------------------------------"
gcloud workflows deploy document-processing-orchestrator \
  --source=./orchestration/document-processing-orchestrator.yaml \
  --location="${WORKFLOW_LOCATION}" \
  --service-account="${SERVICE_ACCOUNT_EMAIL}" \
  --quiet

# --- Dynamically set the worker URL for the workflow ---
echo "--> Setting PDF_SPLITTER_WORKER_URL environment variable for the workflow..."
PDF_SPLITTER_URL=$(gcloud functions describe SplitAndPublish --region "${REGION}" --format 'value(serviceConfig.uri)')
gcloud workflows deploy document-processing-orchestrator \
    --source=./orchestration/document-processing-orchestrator.yaml \
    --location="${WORKFLOW_LOCATION}" \
    --service-account="${SERVICE_ACCOUNT_EMAIL}" \
    --set-env-vars="PDF_SPLITTER_WORKER_URL=${PDF_SPLITTER_URL}" \
    --quiet


echo "✅ Deployment complete."