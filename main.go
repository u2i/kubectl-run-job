package main

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	container "cloud.google.com/go/container/apiv1"
	"cloud.google.com/go/container/apiv1/containerpb"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	ctx := context.Background()

	// Get configuration from environment
	image := os.Getenv("KUBECTL_RUN_IMAGE")
	if image == "" {
		return fmt.Errorf("KUBECTL_RUN_IMAGE environment variable is required")
	}

	project := getEnvOrDefault("KUBECTL_RUN_PROJECT", "c-retrotool-nonprod")
	region := getEnvOrDefault("KUBECTL_RUN_REGION", "europe-west1")
	cluster := getEnvOrDefault("KUBECTL_RUN_CLUSTER", "retrotool-cluster")
	entrypoint := os.Getenv("KUBECTL_RUN_ENTRYPOINT")

	// Build command from args
	var command []string
	if entrypoint != "" {
		command = append(command, entrypoint)
	}
	command = append(command, os.Args[1:]...)

	// Get cluster config using GKE API
	fmt.Println("Authenticating with cluster...")
	config, err := getClusterConfig(ctx, project, region, cluster)
	if err != nil {
		return fmt.Errorf("failed to get cluster config: %w", err)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return fmt.Errorf("failed to create client: %w", err)
	}

	// Create Job
	ttl := int32(300)
	backoff := int32(0)
	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "cloudbuild-job-",
		},
		Spec: batchv1.JobSpec{
			BackoffLimit:            &backoff,
			TTLSecondsAfterFinished: &ttl,
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					RestartPolicy: corev1.RestartPolicyNever,
					NodeSelector: map[string]string{
						"cloud.google.com/gke-ephemeral-storage-local-ssd": "true",
						"cloud.google.com/machine-family":                  "c4",
					},
					Containers: []corev1.Container{
						{
							Name:    "runner",
							Image:   image,
							Command: command,
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:              resource.MustParse("2"),
									corev1.ResourceMemory:           resource.MustParse("8Gi"),
									corev1.ResourceEphemeralStorage: resource.MustParse("10Gi"),
								},
								Limits: corev1.ResourceList{
									corev1.ResourceCPU:              resource.MustParse("4"),
									corev1.ResourceMemory:           resource.MustParse("16Gi"),
									corev1.ResourceEphemeralStorage: resource.MustParse("20Gi"),
								},
							},
						},
					},
				},
			},
		},
	}

	createdJob, err := clientset.BatchV1().Jobs("default").Create(ctx, job, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("failed to create job: %w", err)
	}

	jobName := createdJob.Name
	fmt.Printf("Created job.batch/%s\n", jobName)

	// Wait for pod to be created
	fmt.Println("Waiting for pod...")
	var podName string
	for i := 0; i < 30; i++ {
		pods, err := clientset.CoreV1().Pods("default").List(ctx, metav1.ListOptions{
			LabelSelector: "job-name=" + jobName,
		})
		if err == nil && len(pods.Items) > 0 {
			podName = pods.Items[0].Name
			fmt.Printf("Pod found: pod/%s\n", podName)
			break
		}
		time.Sleep(1 * time.Second)
	}

	if podName == "" {
		return fmt.Errorf("pod not found after 30s")
	}

	// Wait for pod to be running
	fmt.Println("Waiting for pod to be running...")
	for i := 0; i < 300; i++ {
		pod, err := clientset.CoreV1().Pods("default").Get(ctx, podName, metav1.GetOptions{})
		if err == nil && pod.Status.Phase == corev1.PodRunning {
			fmt.Println("Pod is running")
			break
		}
		if i == 299 {
			return fmt.Errorf("pod did not start running after 5 minutes")
		}
		time.Sleep(1 * time.Second)
	}

	// Start streaming logs in background
	logsDone := make(chan error, 1)
	go func() {
		fmt.Println("Starting log stream...")
		req := clientset.CoreV1().Pods("default").GetLogs(podName, &corev1.PodLogOptions{
			Follow: true,
		})
		stream, err := req.Stream(ctx)
		if err != nil {
			logsDone <- fmt.Errorf("failed to get logs: %w", err)
			return
		}
		defer stream.Close()

		fmt.Println("----------------------------------------")
		io.Copy(os.Stdout, stream)
		fmt.Println("----------------------------------------")
		logsDone <- nil
	}()

	// Wait for job completion
	fmt.Println("Waiting for job completion...")
	watcher, err := clientset.BatchV1().Jobs("default").Watch(ctx, metav1.ListOptions{
		FieldSelector: "metadata.name=" + jobName,
	})
	if err != nil {
		return fmt.Errorf("failed to watch job: %w", err)
	}
	defer watcher.Stop()

	var jobFailed bool
	timeoutMinutes := getTimeoutMinutes()
	timeout := time.After(time.Duration(timeoutMinutes) * time.Minute)
	for {
		select {
		case event := <-watcher.ResultChan():
			if event.Type == watch.Modified || event.Type == watch.Added {
				job, ok := event.Object.(*batchv1.Job)
				if !ok {
					continue
				}
				if job.Status.Succeeded > 0 || job.Status.Failed > 0 {
					jobFailed = job.Status.Failed > 0
					goto waitForLogs
				}
			}
		case <-timeout:
			return fmt.Errorf("job did not complete within timeout")
		}
	}

waitForLogs:
	// Wait for logs to finish streaming
	if err := <-logsDone; err != nil {
		return err
	}

	if jobFailed {
		return fmt.Errorf("job failed")
	}
	return nil
}

func getEnvOrDefault(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func getTimeoutMinutes() int {
	timeoutStr := getEnvOrDefault("KUBECTL_RUN_TIMEOUT_MINUTES", "20")
	timeout := 20
	if _, err := fmt.Sscanf(timeoutStr, "%d", &timeout); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: invalid KUBECTL_RUN_TIMEOUT_MINUTES value '%s', using default 20 minutes\n", timeoutStr)
		return 20
	}
	if timeout <= 0 {
		fmt.Fprintf(os.Stderr, "Warning: KUBECTL_RUN_TIMEOUT_MINUTES must be positive, using default 20 minutes\n")
		return 20
	}
	return timeout
}

func getClusterConfig(ctx context.Context, project, location, clusterName string) (*rest.Config, error) {
	// Create GKE client
	client, err := container.NewClusterManagerClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create cluster manager client: %w", err)
	}
	defer client.Close()

	// Get cluster info
	req := &containerpb.GetClusterRequest{
		Name: fmt.Sprintf("projects/%s/locations/%s/clusters/%s", project, location, clusterName),
	}
	cluster, err := client.GetCluster(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to get cluster: %w", err)
	}

	// Decode CA certificate
	caCert, err := base64.StdEncoding.DecodeString(cluster.MasterAuth.ClusterCaCertificate)
	if err != nil {
		return nil, fmt.Errorf("failed to decode CA cert: %w", err)
	}

	// Get Google Cloud default credentials
	ts, err := google.DefaultTokenSource(ctx, "https://www.googleapis.com/auth/cloud-platform")
	if err != nil {
		return nil, fmt.Errorf("failed to get token source: %w", err)
	}

	// Build kubeconfig with authentication
	config := &rest.Config{
		Host: "https://" + cluster.Endpoint,
		TLSClientConfig: rest.TLSClientConfig{
			CAData: caCert,
		},
		WrapTransport: func(rt http.RoundTripper) http.RoundTripper {
			return &oauth2.Transport{
				Source: ts,
				Base:   rt,
			}
		},
	}

	return config, nil
}
