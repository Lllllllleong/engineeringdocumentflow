#!/bin/bash
# =============================================================================
#         Deployment Script for the PDF Splitter Cloud Function
# =============================================================================
# This script prepares and deploys only the 'pdf-splitter' function.
# =============================================================================

# Exit immediately if a command exits with a non-zero status.
set -e

echo "--- Loading environment variables..."
# --- Load Environment Configuration ---
source ./scripts/setup_env.sh

# --- Define the function to be deployed ---
FUNCTION_NAME="pdf-splitter"
GCLOUD_FUNCTION_NAME="SplitAndPublish" # The name for the function in GCP

# --- Prepare the build directory ---
BUILD_DIR="/tmp/build/${FUNCTION_NAME}"

echo "-----------------------------------------------------"
echo ">>> Preparing build directory for ${FUNCTION_NAME}"
echo "-----------------------------------------------------"

rm -rf "${BUILD_DIR}"
mkdir -p "${BUILD_DIR}"

# 1. Copy the function's specific code and all shared internal code.
echo "--> Copying source files..."
cp -r "cmd/${FUNCTION_NAME}/." "${BUILD_DIR}/"
cp -r internal "${BUILD_DIR}/"

echo "--- Vendoring dependencies for ${FUNCTION_NAME} ---"
# 2. Create a self-contained Go module and vendor dependencies.
(
  cd "${BUILD_DIR}"
  # THIS IS THE FIX: Initialize with a local, non-conflicting module name.
  go mod init "github.com/Lllllllleong/engineeringdocumentflow"
  go mod tidy
  go mod vendor
)

echo "--- Deploying function: ${GCLOUD_FUNCTION_NAME} ---"
# 3. Deploy the function using the prepared build directory.
gcloud functions deploy "${GCLOUD_FUNCTION_NAME}" \
  --gen2 \
  --runtime=go123 \
  --region="${REGION}" \
  --source="${BUILD_DIR}" \
  --entry-point=SplitAndPublish \
  --trigger-event-filters="type=google.cloud.storage.object.v1.finalized" \
  --trigger-event-filters="bucket=${UPLOADS_BUCKET}" \
  --service-account="${SERVICE_ACCOUNT_EMAIL}" \
  --quiet

echo "-----------------------------------------------------"
echo "✅ Deployment of ${GCLOUD_FUNCTION_NAME} complete."
echo "-----------------------------------------------------"