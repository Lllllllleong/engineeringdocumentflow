#!/bin/bash
# =============================================================================
#           DEPLOY SCRIPT FOR '1-pdf-splitter'
# =============================================================================
# Deploys the pdf-splitter function using the best practices from the original
# script, but updated for the new directory structure and environment variables.
#
# INSTRUCTIONS:
# 1. Run `source ./scripts/setup_env.sh` first.
# 2. Run this script from the project root: `./scripts/deploy.sh`
# =============================================================================

# Exit immediately if a command exits with a non-zero status.
set -e

echo "Sourcing environment variables..."
# This ensures the script has access to PROJECT_ID, REGION, etc.
source ./scripts/setup_env.sh

echo ""
echo "Starting deployment of 'pdf-splitter-v2' function..."
echo "  Project:          ${PROJECT_ID}"
echo "  Region:           ${REGION}"
echo "  Source Path:      ./services/1-pdf-splitter/" #<-- CONFIRMING NEW PATH
echo "  Trigger Bucket:   ${TRIGGER_BUCKET}"
echo "  Service Account:  ${SERVICE_ACCOUNT_EMAIL}"
echo ""

# The full, correct deployment command
gcloud functions deploy pdf-splitter-v2 \
  --gen2 \
  --runtime=go123 \
  --project=${PROJECT_ID} \
  --region=${REGION} \
  --source=./services/1-pdf-splitter/ \
  --entry-point=SplitAndPublish \
  --trigger-resource=${TRIGGER_BUCKET} \
  --trigger-event=google.cloud.storage.object.v1.finalized \
  --service-account=${SERVICE_ACCOUNT_EMAIL} \
  --timeout=540s \
  --memory=1Gi \
  --update-labels=app=pdf-pipeline,service=splitter \
  --set-env-vars="SPLIT_PAGES_BUCKET=${SPLIT_PAGES_BUCKET},FIRESTORE_COLLECTION=${FIRESTORE_COLLECTION},WORKFLOW_LOCATION=${WORKFLOW_LOCATION},WORKFLOW_ID=${WORKFLOW_ID}"

echo ""
echo "✅ Deployment command submitted successfully."
echo "Check the Google Cloud Console for deployment progress and logs."