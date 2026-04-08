# kubectl-run-job

A Kubernetes job runner for GKE that provides `kubectl run`-like functionality with real-time log streaming.

## Overview

`kubectl-run-job` is a Go-based tool that creates and monitors Kubernetes Jobs on GKE (Google Kubernetes Engine), streaming logs in real-time. It's particularly useful for running CI/CD tasks, one-off jobs, or any workload that needs to run to completion with live output.

## Features

- **GKE Authentication**: Automatically authenticates with GKE clusters using GCP credentials
- **Real-time Log Streaming**: Streams job logs as they're produced with `Follow: true`
- **Job Lifecycle Management**: Creates, monitors, and cleans up Kubernetes Jobs
- **Local SSD Support**: Configurable node selectors for ephemeral storage on Local SSD-backed nodes
- **Resource Configuration**: Customizable CPU, memory, and ephemeral storage requests/limits
- **Auto-cleanup**: Jobs automatically clean up after 5 minutes (configurable TTL)

## Usage

### Environment Variables

- `KUBECTL_RUN_IMAGE` (required): Container image to run
- `KUBECTL_RUN_PROJECT` (default: `c-retrotool-nonprod`): GCP project ID
- `KUBECTL_RUN_REGION` (default: `europe-west1`): GKE cluster region
- `KUBECTL_RUN_CLUSTER` (default: `retrotool-cluster`): GKE cluster name
- `KUBECTL_RUN_ENTRYPOINT`: Optional entrypoint override
- `KUBECTL_RUN_TIMEOUT_MINUTES` (default: `20`): Job completion timeout in minutes

### Examples

```bash
# Run a simple job
export KUBECTL_RUN_IMAGE=gcr.io/project/image:tag
kubectl-run-job echo "Hello World"

# Run with custom entrypoint
export KUBECTL_RUN_ENTRYPOINT=/bin/bash
kubectl-run-job -c "npm test"

# Run against a specific cluster
export KUBECTL_RUN_PROJECT=my-project
export KUBECTL_RUN_REGION=us-central1
export KUBECTL_RUN_CLUSTER=my-cluster
export KUBECTL_RUN_IMAGE=my-image:latest
kubectl-run-job npm run e2e
```

## Building

### Local Build

```bash
go build -o kubectl-run-job .
```

### Docker Build

```bash
docker build -t kubectl-run-job .
```

### Google Cloud Build

```bash
gcloud builds submit --config=cloudbuild.yaml
```

## Architecture

The tool performs the following steps:

1. **Authenticate**: Uses GKE API to get cluster credentials
2. **Create Job**: Creates a Kubernetes Job with specified container and command
3. **Wait for Pod**: Polls until the job's pod is created
4. **Wait for Running**: Waits for pod to reach Running state (up to 5 minutes)
5. **Stream Logs**: Starts streaming logs in a background goroutine with `Follow: true`
6. **Monitor Completion**: Watches job status for completion or failure
7. **Wait for Logs**: Ensures all logs are streamed before exiting
8. **Auto-cleanup**: Job TTL automatically deletes completed jobs after 5 minutes

## Configuration

The job is created with these defaults:

- **Node Selector**:
  - `cloud.google.com/gke-ephemeral-storage-local-ssd: "true"`
  - `cloud.google.com/machine-family: "c4"`

- **Resource Requests**:
  - CPU: 2 cores
  - Memory: 8Gi
  - Ephemeral Storage: 10Gi

- **Resource Limits**:
  - CPU: 4 cores
  - Memory: 16Gi
  - Ephemeral Storage: 20Gi

- **Other Settings**:
  - BackoffLimit: 0 (no retries)
  - TTLSecondsAfterFinished: 300 (5 minutes)
  - RestartPolicy: Never

## Development

### Prerequisites

- Go 1.21 or later
- GCP credentials with GKE access
- kubectl configured for your cluster (optional, for comparison)

### Running Tests

```bash
go test ./...
```

## License

MIT

## Contributing

Contributions are welcome! Please feel free to submit a Pull Request.
