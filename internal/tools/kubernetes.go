package tools

import (
	"context"
	"fmt"
	"strings"
	"time"

	"path/filepath"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/homedir"
)

// KubernetesClient wraps the k8s client-go clientset.
type KubernetesClient struct {
	clientset *kubernetes.Clientset
}

// NewKubernetesClient creates a client using in-cluster config when
// available (i.e. running as a pod), falling back to kubeconfig for
// local development.
func NewKubernetesClient() (*KubernetesClient, error) {
	config, err := rest.InClusterConfig()
	if err != nil {
		// Not in a cluster — try kubeconfig
		kubeconfig := filepath.Join(homedir.HomeDir(), ".kube", "config")
		config, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
		if err != nil {
			return nil, fmt.Errorf("building kubeconfig: %w", err)
		}
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("creating k8s clientset: %w", err)
	}

	return &KubernetesClient{clientset: clientset}, nil
}

// PodStatusSummary holds the aggregated pod health data.
type PodStatusSummary struct {
	ServiceName       string
	Namespace         string
	TotalPods         int
	RunningPods       int
	PendingPods       int
	CrashLoopBackOff  int
	OOMKilled         int
	LastRestartTime   *time.Time
	RolloutInProgress bool
}

// GetPodStatus returns a human-readable pod health summary for a service.
// namespace defaults to "default" if empty.
func (c *KubernetesClient) GetPodStatus(
	ctx context.Context,
	service, namespace string,
) (string, error) {
	if namespace == "" {
		namespace = "default"
	}

	// List pods whose name contains the service name
	pods, err := c.clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: fmt.Sprintf("app=%s", service),
	})
	if err != nil {
		// Label selector failed — try name prefix fallback
		allPods, err2 := c.clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{})
		if err2 != nil {
			return "", fmt.Errorf("listing pods: %w", err)
		}
		// Filter manually by name prefix
		var filtered []corev1.Pod
		for _, p := range allPods.Items {
			if strings.HasPrefix(p.Name, service) {
				filtered = append(filtered, p)
			}
		}
		pods = &corev1.PodList{Items: filtered}
	}

	summary := analysePods(service, namespace, pods.Items)
	return formatPodStatus(summary), nil
}

func analysePods(service, namespace string, pods []corev1.Pod) PodStatusSummary {
	s := PodStatusSummary{
		ServiceName: service,
		Namespace:   namespace,
		TotalPods:   len(pods),
	}

	for _, pod := range pods {
		switch pod.Status.Phase {
		case corev1.PodRunning:
			s.RunningPods++
		case corev1.PodPending:
			s.PendingPods++
		}

		for _, cs := range pod.Status.ContainerStatuses {
			// CrashLoopBackOff
			if cs.State.Waiting != nil &&
				cs.State.Waiting.Reason == "CrashLoopBackOff" {
				s.CrashLoopBackOff++
			}

			// OOMKilled — appears in LastTerminationState
			if cs.LastTerminationState.Terminated != nil &&
				cs.LastTerminationState.Terminated.Reason == "OOMKilled" {
				s.OOMKilled++
			}

			// Track most recent restart time
			if cs.LastTerminationState.Terminated != nil {
				t := cs.LastTerminationState.Terminated.FinishedAt.Time
				if s.LastRestartTime == nil || t.After(*s.LastRestartTime) {
					s.LastRestartTime = &t
				}
			}
		}
	}

	return s
}

func formatPodStatus(s PodStatusSummary) string {
	if s.TotalPods == 0 {
		return fmt.Sprintf(
			"No pods found for service %q in namespace %q. "+
				"The service may not be deployed to this cluster or namespace.",
			s.ServiceName, s.Namespace,
		)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Pod status for service %q (namespace: %s):\n",
		s.ServiceName, s.Namespace)
	fmt.Fprintf(&b, "  Total pods: %d | Running: %d | Pending: %d\n",
		s.TotalPods, s.RunningPods, s.PendingPods)

	if s.CrashLoopBackOff > 0 {
		fmt.Fprintf(&b, "  *** WARNING: %d pod(s) in CrashLoopBackOff ***\n",
			s.CrashLoopBackOff)
	}
	if s.OOMKilled > 0 {
		fmt.Fprintf(&b, "  *** WARNING: %d pod(s) recently OOMKilled ***\n",
			s.OOMKilled)
	}
	if s.LastRestartTime != nil {
		ago := time.Since(*s.LastRestartTime).Round(time.Second)
		fmt.Fprintf(&b, "  Last container restart: %s ago (at %s UTC)\n",
			ago, s.LastRestartTime.UTC().Format("15:04:05"))
	}
	if s.RolloutInProgress {
		b.WriteString("  *** Rolling update in progress ***\n")
	}
	if s.CrashLoopBackOff == 0 && s.OOMKilled == 0 && s.PendingPods == 0 {
		b.WriteString("  Pod health looks normal.\n")
	}

	return b.String()
}
