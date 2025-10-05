#!/bin/bash
set -euo pipefail

# This script wraps a container command to run it as a Kubernetes Job on GKE
# Usage: Similar to running a docker container in Cloud Build
# Environment variables:
#   KUBECTL_RUN_IMAGE: Container image to run (required)
#   KUBECTL_RUN_PROJECT: GCP project (default: c-retrotool-nonprod)
#   KUBECTL_RUN_REGION: GCP region (default: europe-west1)
#   KUBECTL_RUN_CLUSTER: GKE cluster name (default: retrotool-cluster)
#   KUBECTL_RUN_ENTRYPOINT: Container entrypoint (optional)

IMAGE="${KUBECTL_RUN_IMAGE:?KUBECTL_RUN_IMAGE environment variable is required}"
PROJECT_ID="${KUBECTL_RUN_PROJECT:-c-retrotool-nonprod}"
REGION="${KUBECTL_RUN_REGION:-europe-west1}"
CLUSTER="${KUBECTL_RUN_CLUSTER:-retrotool-cluster}"

# Build command array from script arguments
COMMAND=()
if [ -n "${KUBECTL_RUN_ENTRYPOINT:-}" ]; then
  COMMAND+=("${KUBECTL_RUN_ENTRYPOINT}")
fi
COMMAND+=("$@")

# Generate Job manifest
cat > /tmp/job.yaml <<EOF
apiVersion: batch/v1
kind: Job
metadata:
  generateName: cloudbuild-job-
spec:
  backoffLimit: 0
  ttlSecondsAfterFinished: 300
  template:
    spec:
      restartPolicy: Never
      containers:
      - name: runner
        image: ${IMAGE}
EOF

# Add command if provided
if [ ${#COMMAND[@]} -gt 0 ]; then
  echo "        command:" >> /tmp/job.yaml
  for cmd in "${COMMAND[@]}"; do
    # Use yq/proper YAML encoding, or just write literal strings for simple cases
    # For now, use proper YAML literal block scalar for multi-line strings
    if [[ "$cmd" =~ $'\n' ]]; then
      echo "        - |" >> /tmp/job.yaml
      echo "$cmd" | sed 's/^/          /' >> /tmp/job.yaml
    else
      printf "        - %s\n" "$(echo "$cmd" | sed 's/"/\\"/g' | sed "s/'/\\\\'/g")" >> /tmp/job.yaml
    fi
  done
fi

# Get cluster credentials
echo "Authenticating with cluster..."
gcloud container clusters get-credentials \
  --project="${PROJECT_ID}" \
  --region="${REGION}" \
  "${CLUSTER}"

# Create Job and capture name
JOB=$(kubectl create -f /tmp/job.yaml -o name)
echo "Created ${JOB}"

# Extract job name without prefix
JOB_NAME=${JOB#job.batch/}

# Wait for pod to be created
echo "Waiting for pod..."
for i in {1..30}; do
  POD=$(kubectl get pods -l job-name=${JOB_NAME} -o name 2>/dev/null | head -1)
  if [ -n "${POD}" ]; then
    echo "Pod found: ${POD}"
    break
  fi
  sleep 1
done

if [ -z "${POD}" ]; then
  echo "ERROR: Pod not found after 30s"
  exit 1
fi

# Wait for container to start
echo "Waiting for container to start..."
kubectl wait --for=condition=Ready --timeout=120s ${POD} || true

# Stream logs (will block until pod completes)
kubectl logs -f ${POD}

# Wait for job completion
kubectl wait --for=condition=complete --timeout=600s ${JOB}
