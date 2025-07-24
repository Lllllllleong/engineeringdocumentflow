#!/bin/bash
# =============================================================================
#           PERMISSION SETUP SCRIPT FOR THE DOCUMENT PROCESSING PIPELINE
# =============================================================================
#
# INSTRUCTIONS:
# 1. This script should be run ONCE to set the initial permissions.
# 2. Make sure you have set your environment by running:
#    source ./scripts/setup_env.sh
# 3. Run this script from the project root: ./scripts/setup_permissions.sh
#
# =============================================================================

# Exit immediately if a command exits with a non-zero status.
set -e

echo "Sourcing environment variables..."
source ./scripts/setup_env.sh

echo ""
echo "--- Setting permissions for service account: ${SERVICE_ACCOUNT_EMAIL} ---"

# WARNING: As per our plan, we are using the broad 'editor' role for early
# development. This is NOT secure for production. Before production, this MUST
# be replaced with more specific roles.
echo "Assigning temporary broad role: roles/editor..."
gcloud projects add-iam-policy-binding ${PROJECT_ID} \
  --member="serviceAccount:${SERVICE_ACCOUNT_EMAIL}" \
  --role="roles/editor" \
  --condition=None > /dev/null

# The function still needs to write to Firestore. This permission is correct.
echo "Assigning role: roles/datastore.user..."
gcloud projects add-iam-policy-binding ${PROJECT_ID} \
  --member="serviceAccount:${SERVICE_ACCOUNT_EMAIL}" \
  --role="roles/datastore.user" \
  --condition=None > /dev/null

# NEW: The function's most important new permission is to trigger the workflow.
echo "Assigning NEW role: roles/workflows.invoker..."
gcloud projects add-iam-policy-binding ${PROJECT_ID} \
  --member="serviceAccount:${SERVICE_ACCOUNT_EMAIL}" \
  --role="roles/workflows.invoker" \
  --condition=None > /dev/null

# The Eventarc and Cloud Storage service account permissions are no longer
# needed for this architecture, so they have been removed.

echo ""
echo "✅ All necessary IAM permissions for 'pdf-splitter-v2' have been configured."