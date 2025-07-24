#!/bin/bash

# ==============================================================================
# WARNING: THIS IS A DESTRUCTIVE SCRIPT.
# It will PERMANENTLY DELETE all data from the GCS buckets and the
# Firestore collection used by the document processing pipeline.
#
# DO NOT run this in a production environment.
# Use only for resetting a development or testing environment.
# ==============================================================================

# Exit immediately if any command fails.
set -e

# --- Load Environment Configuration ---
echo "Sourcing environment variables from scripts/setup_env.sh..."
source ./scripts/setup_env.sh

# --- User Confirmation ---
# This is a critical safety check. The script will not proceed
# unless the user explicitly types "yes".
echo ""
echo "You are about to permanently delete all data from the following resources:"
echo "  - Ingest Bucket:      gs://${TRIGGER_BUCKET}/"
echo "  - Split Pages Bucket: gs://${SPLIT_PAGES_BUCKET}/"
echo "  - Firestore Collection: ${FIRESTORE_COLLECTION}"
echo ""
read -p "Are you sure you want to continue? (Type 'yes' to proceed): " CONFIRMATION

if [ "$CONFIRMATION" != "yes" ]; then
    echo "Confirmation not received. Aborting script."
    exit 1
fi

# --- Validate Environment Variables ---
if [ -z "$TRIGGER_BUCKET" ]; then
    echo "Error: TRIGGER_BUCKET is not set. Please check your env.sh file."
    exit 1
fi

if [ -z "$SPLIT_PAGES_BUCKET" ]; then
    echo "Error: SPLIT_PAGES_BUCKET is not set. Please check your env.sh file."
    exit 1
fi

if [ -z "$FIRESTORE_COLLECTION" ]; then
    echo "Error: FIRESTORE_COLLECTION is not set. Please check your env.sh file."
    exit 1
fi

# --- Deletion Logic ---

echo ""
echo "Proceeding with deletion..."

# 1. Delete all objects from the Ingest Bucket
# The `rm -r` command recursively removes all objects and subdirectories.
# The `*` at the end means "everything inside the bucket".
# The `|| true` ensures the script doesn't fail if the bucket is already empty.
echo "Deleting all files from ingest bucket: gs://${TRIGGER_BUCKET}/"
gcloud storage rm -r "gs://${TRIGGER_BUCKET}/*" || true
echo "✅ Ingest bucket cleared."


# 2. Delete all objects from the Split Pages Bucket
echo "Deleting all files from split pages bucket: gs://${SPLIT_PAGES_BUCKET}/"
gcloud storage rm -r "gs://${SPLIT_PAGES_BUCKET}/*" || true
echo "✅ Split pages bucket cleared."



# 3. Delete all documents from the Firestore Collection (CORRECTED COMMAND)
# The `gcloud firestore bulk-delete` command is designed to wipe out
# an entire collection. This is a powerful, non-reversible operation.
echo "Submitting bulk-delete job for Firestore collection: ${FIRESTORE_COLLECTION}"
gcloud firestore bulk-delete --collection-ids="${FIRESTORE_COLLECTION}" --quiet
echo "✅ Firestore bulk-delete job submitted."

echo ""
echo "Cleanup complete. The environment has been reset."
