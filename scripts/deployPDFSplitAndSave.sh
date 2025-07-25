#!/bin/bash
# =============================================================================
#         Deployment Script for the PDF Split and Save Cloud Function
# =============================================================================
# This script deploys only the 'pdfSplitAndSave' function.
# =============================================================================

# Exit immediately if a command exits with a non-zero status.
set -e

echo "--- Loading environment variables..."
# --- Load Environment Configuration ---
source ./scripts/setup_env.sh

# --- Define the function to be deployed ---
FUNCTION_NAME="pdfSplitAndSave"
GCLOUD_FUNCTION_NAME="pdfSplitAndSave" # The name for the function in GCP

echo "--- Deploying function: ${GCLOUD_FUNCTION_NAME} ---"
# Deploy the function using the 'pdf-splitter' directory as the source.
gcloud functions deploy "${GCLOUD_FUNCTION_NAME}" \
  --gen2 \
  --runtime=go123 \
  --region="${REGION}" \
  --source="./${FUNCTION_NAME}" \
  --entry-point=SplitAndPublish \
  --trigger-event-filters="type=google.cloud.storage.object.v1.finalized" \
  --trigger-event-filters="bucket=${UPLOADS_BUCKET}" \
  --service-account="${SERVICE_ACCOUNT_EMAIL}" \
  --set-env-vars="PROJECT_ID=${PROJECT_ID},SPLIT_PAGES_BUCKET=${SPLIT_PAGES_BUCKET},FIRESTORE_COLLECTION=${FIRESTORE_COLLECTION}" \
  --quiet

echo "-----------------------------------------------------"
echo "✅ Deployment of ${GCLOUD_FUNCTION_NAME} complete."
echo "-----------------------------------------------------"