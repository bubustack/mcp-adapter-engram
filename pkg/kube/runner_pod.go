package kube

import (
	"context"
	"fmt"
	"os"
	"time"

	v1alpha1 "github.com/bubustack/bobrapet/api/v1alpha1"
	sdkk8s "github.com/bubustack/bubu-sdk-go/k8s"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// RunnerPodSpec describes the runner Pod to create for stdio transport.
type RunnerPodSpec struct {
	Namespace                     string
	Name                          string
	Image                         string
	PullPolicy                    string
	Command                       []string
	Args                          []string
	Env                           map[string]string
	Resources                     *v1alpha1.WorkloadResources
	Security                      *v1alpha1.WorkloadSecurity
	NodeSelector                  map[string]string
	Tolerations                   []corev1.Toleration
	Labels                        map[string]string
	Annotations                   map[string]string
	OwnerPodName                  string
	OwnerPodUID                   string
	TerminationGracePeriodSeconds *int64
	// If true, env values are materialized into a per-run Secret and mounted via EnvFrom.
	UseEphemeralSecret bool
	// If provided, refer to an existing secret by name instead of creating an ephemeral one.
	// When set, this secret will be attached via EnvFrom and takes precedence over Env map.
	ExistingEnvSecretName string
	// Map of desired env var names -> key names inside ExistingEnvSecretName
	ExistingEnvKeys map[string]string
}

// EnsureRunnerPod creates the runner Pod if it doesn't exist. Returns (created, ephemeralSecretName, error).
func EnsureRunnerPod(ctx context.Context, spec RunnerPodSpec) (bool, string, error) {
	if spec.Namespace == "" || spec.Name == "" {
		return false, "", fmt.Errorf("namespace and name are required")
	}
	if spec.Image == "" {
		return false, "", fmt.Errorf("stdio.image is required")
	}
	k, err := sdkk8s.NewClient()
	if err != nil {
		return false, "", err
	}

	ownerRef, err := getOwnerRef(ctx, k, spec)
	if err != nil {
		return false, "", err
	}

	// Short-circuit if already exists
	exists := podExists(ctx, k, spec.Namespace, spec.Name)
	if exists {
		return false, "", nil
	}

	rr := buildResourceRequirements(spec.Resources)
	sc := buildSecurityContext(spec.Security)
	labels := buildLabels(spec.Labels)
	annotations := buildAnnotations(spec.Annotations, spec)

	ephemeralSecretName, envFrom, envVars, err := buildEnvSources(ctx, k, spec, ownerRef)
	if err != nil {
		return false, "", err
	}

	pod := buildRunnerPod(spec, rr, sc, labels, annotations, envFrom, envVars, ownerRef)
	if err := k.Create(ctx, pod); err != nil {
		return false, "", fmt.Errorf("create runner pod: %w", err)
	}
	return true, ephemeralSecretName, nil
}

func getOwnerRef(ctx context.Context, k *sdkk8s.Client, spec RunnerPodSpec) (*metav1.OwnerReference, error) {
	if spec.OwnerPodName == "" {
		return nil, nil
	}
	ownerPod := &corev1.Pod{}
	if err := k.Get(ctx, types.NamespacedName{Namespace: spec.Namespace, Name: spec.OwnerPodName}, ownerPod); err != nil {
		return nil, err
	}
	ref := &metav1.OwnerReference{APIVersion: "v1", Kind: "Pod", Name: ownerPod.Name, UID: ownerPod.UID}
	return ref, nil
}

func podExists(ctx context.Context, k *sdkk8s.Client, namespace, name string) bool {
	pod := &corev1.Pod{}
	return k.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, pod) == nil
}

func buildResourceRequirements(wr *v1alpha1.WorkloadResources) corev1.ResourceRequirements {
	rr := corev1.ResourceRequirements{}
	if wr == nil {
		return rr
	}
	// requests
	if r := wr.Requests; r != nil {
		rr.Requests = ensureResList(rr.Requests)
		addQty(&rr.Requests, corev1.ResourceCPU, r.CPU)
		addQty(&rr.Requests, corev1.ResourceMemory, r.Memory)
		addQty(&rr.Requests, corev1.ResourceEphemeralStorage, r.EphemeralStorage)
	}
	// limits
	if l := wr.Limits; l != nil {
		rr.Limits = ensureResList(rr.Limits)
		addQty(&rr.Limits, corev1.ResourceCPU, l.CPU)
		addQty(&rr.Limits, corev1.ResourceMemory, l.Memory)
		addQty(&rr.Limits, corev1.ResourceEphemeralStorage, l.EphemeralStorage)
	}
	return rr
}

func ensureResList(rl corev1.ResourceList) corev1.ResourceList {
	if rl == nil {
		return corev1.ResourceList{}
	}
	return rl
}

func addQty(rl *corev1.ResourceList, name corev1.ResourceName, ptr *string) {
	if ptr == nil {
		return
	}
	if q, err := parseQuantity(*ptr); err == nil {
		(*rl)[name] = q
	}
}

func buildSecurityContext(ws *v1alpha1.WorkloadSecurity) *corev1.SecurityContext {
	if ws == nil {
		return nil
	}
	sctx := &corev1.SecurityContext{}
	if ws.RunAsNonRoot != nil {
		sctx.RunAsNonRoot = ws.RunAsNonRoot
	}
	if ws.ReadOnlyRootFilesystem != nil {
		sctx.ReadOnlyRootFilesystem = ws.ReadOnlyRootFilesystem
	}
	if ws.AllowPrivilegeEscalation != nil {
		sctx.AllowPrivilegeEscalation = ws.AllowPrivilegeEscalation
	}
	if ws.RunAsUser != nil {
		sctx.RunAsUser = ws.RunAsUser
	}
	return sctx
}

func buildLabels(extra map[string]string) map[string]string {
	labels := CommonLabels("")
	for k, v := range extra {
		labels[k] = v
	}
	if v := os.Getenv("BUBU_STEPRUN_NAME"); v != "" {
		labels["bubustack.io/stepRun"] = v
	}
	return labels
}

func buildAnnotations(extra map[string]string, spec RunnerPodSpec) map[string]string {
	annotations := map[string]string{}
	for k, v := range extra {
		annotations[k] = v
	}
	annotations["mcp.bubustack.io/spec-hash"] = SpecHash(spec)
	return annotations
}

func buildEnvSources(
	ctx context.Context,
	k *sdkk8s.Client,
	spec RunnerPodSpec,
	ownerRef *metav1.OwnerReference,
) (string, []corev1.EnvFromSource, []corev1.EnvVar, error) {
	envFrom := []corev1.EnvFromSource{}
	envVars := []corev1.EnvVar{}
	ephemeralSecretName := ""
	if spec.ExistingEnvSecretName != "" && !spec.UseEphemeralSecret {
		if len(spec.ExistingEnvKeys) > 0 {
			for envName, keyName := range spec.ExistingEnvKeys {
				envVars = append(envVars, corev1.EnvVar{
					Name: envName,
					ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{
						LocalObjectReference: corev1.LocalObjectReference{Name: spec.ExistingEnvSecretName},
						Key:                  keyName,
					}},
				})
			}
		} else {
			envFrom = append(envFrom, corev1.EnvFromSource{
				SecretRef: &corev1.SecretEnvSource{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: spec.ExistingEnvSecretName,
					},
				},
			})
		}
		// If an existing secret is used, do not create an ephemeral secret.
		return "", envFrom, envVars, nil
	}
	if spec.UseEphemeralSecret && len(spec.Env) > 0 {
		ephemeralSecretName = spec.Name + "-env"
		sec := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: spec.Namespace,
				Name:      ephemeralSecretName,
				Labels: map[string]string{
					"bubustack.io/ownerPod": spec.OwnerPodName,
				},
			},
		}
		sec.Type = corev1.SecretTypeOpaque
		sec.Data = map[string][]byte{}
		for k, v := range spec.Env {
			sec.Data[k] = []byte(v)
		}
		if ownerRef != nil {
			sec.OwnerReferences = []metav1.OwnerReference{*ownerRef}
		}
		if err := k.Create(ctx, sec); err != nil {
			return "", nil, nil, fmt.Errorf("create ephemeral secret: %w", err)
		}
		envFrom = append(envFrom, corev1.EnvFromSource{
			SecretRef: &corev1.SecretEnvSource{LocalObjectReference: corev1.LocalObjectReference{Name: ephemeralSecretName}},
		})
	} else {
		for k, v := range spec.Env {
			envVars = append(envVars, corev1.EnvVar{Name: k, Value: v})
		}
	}
	return ephemeralSecretName, envFrom, envVars, nil
}

func buildRunnerPod(
	spec RunnerPodSpec,
	rr corev1.ResourceRequirements,
	sc *corev1.SecurityContext,
	labels map[string]string,
	annotations map[string]string,
	envFrom []corev1.EnvFromSource,
	envVars []corev1.EnvVar,
	ownerRef *metav1.OwnerReference,
) *corev1.Pod {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:   spec.Namespace,
			Name:        spec.Name,
			Labels:      labels,
			Annotations: annotations,
		},
		Spec: corev1.PodSpec{
			RestartPolicy:                 corev1.RestartPolicyNever,
			NodeSelector:                  spec.NodeSelector,
			Tolerations:                   spec.Tolerations,
			TerminationGracePeriodSeconds: spec.TerminationGracePeriodSeconds,
			Containers: []corev1.Container{{
				Name:            "with-stdio",
				Image:           spec.Image,
				ImagePullPolicy: corev1.PullPolicy(spec.PullPolicy),
				Command:         spec.Command,
				Args:            spec.Args,
				Env:             envVars,
				EnvFrom:         envFrom,
				Stdin:           true,
				StdinOnce:       false,
				TTY:             false,
				Resources:       rr,
				SecurityContext: sc,
			}},
		},
	}
	if ownerRef != nil {
		pod.OwnerReferences = []metav1.OwnerReference{*ownerRef}
	}
	return pod
}

// WaitForPodRunning waits until the pod is Running (or returns error if it fails) with a bounded timeout.
func WaitForPodRunning(ctx context.Context, namespace, name string) error {
	k, err := sdkk8s.NewClient()
	if err != nil {
		return err
	}
	deadline := time.Now().Add(5 * time.Minute)
	for {
		if time.Now().After(deadline) {
			return fmt.Errorf("timeout waiting for pod %s/%s running", namespace, name)
		}
		pod, err := getPod(ctx, k, namespace, name)
		if err != nil {
			if err := waitOrCancel(ctx, 500*time.Millisecond); err != nil {
				return err
			}
			continue
		}
		if isPodReady(pod) {
			return nil
		}
		if isTerminal(pod) {
			return fmt.Errorf("pod %s/%s completed with phase %s", namespace, name, pod.Status.Phase)
		}
		if err := waitOrCancel(ctx, 500*time.Millisecond); err != nil {
			return err
		}
	}
}

func getPod(ctx context.Context, k *sdkk8s.Client, namespace, name string) (*corev1.Pod, error) {
	pod := &corev1.Pod{}
	if err := k.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, pod); err != nil {
		return nil, err
	}
	return pod, nil
}

func isPodReady(pod *corev1.Pod) bool {
	if pod.Status.Phase != corev1.PodRunning {
		return false
	}
	for _, cs := range pod.Status.ContainerStatuses {
		if cs.Name == "with-stdio" && cs.Ready {
			return true
		}
	}
	return false
}

func isTerminal(pod *corev1.Pod) bool {
	return pod.Status.Phase == corev1.PodFailed || pod.Status.Phase == corev1.PodSucceeded
}

func waitOrCancel(ctx context.Context, d time.Duration) error {
	select {
	case <-time.After(d):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// DeleteRunnerPod deletes the runner Pod and optionally the ephemeral Secret.
func DeleteRunnerPod(ctx context.Context, namespace, name, ephemeralSecretName string) error {
	k, err := sdkk8s.NewClient()
	if err != nil {
		return err
	}
	// Delete Pod with foreground propagation
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: name}}
	policy := metav1.DeletePropagationForeground
	if err := k.Delete(ctx, pod, &client.DeleteOptions{PropagationPolicy: &policy}); err != nil {
		return err
	}
	if ephemeralSecretName != "" {
		sec := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: ephemeralSecretName}}
		if err := k.Delete(ctx, sec); err != nil {
			return fmt.Errorf("delete ephemeral secret: %w", err)
		}
	}
	return nil
}

// ReapOldOwnedRunnerPods deletes old pods owned by this adapter pod to avoid zombies.
func ReapOldOwnedRunnerPods(ctx context.Context, namespace, ownerPodName string, olderThan time.Duration) error {
	k, err := sdkk8s.NewClient()
	if err != nil {
		return err
	}
	pods := &corev1.PodList{}
	if err := k.List(ctx, pods, client.InNamespace(namespace), client.MatchingLabels(map[string]string{
		"bubustack.io/ownerPod":  ownerPodName,
		"app.kubernetes.io/name": "mcp-adapter-engram",
	})); err != nil {
		return err
	}
	cutoff := time.Now().Add(-olderThan)
	for _, p := range pods.Items {
		if p.CreationTimestamp.Time.Before(cutoff) {
			_ = k.Delete(ctx, &p)
		}
	}
	return nil
}

// GetOwnPod returns the current adapter Pod using Downward API env vars.
func GetOwnPod(ctx context.Context) (*corev1.Pod, error) {
	ns := os.Getenv("POD_NAMESPACE")
	if ns == "" {
		ns = sdkk8s.ResolvePodNamespace()
	}
	name := os.Getenv("POD_NAME")
	if name == "" {
		return nil, fmt.Errorf("POD_NAME env is not set")
	}
	k, err := sdkk8s.NewClient()
	if err != nil {
		return nil, err
	}
	pod := &corev1.Pod{}
	if err := k.Get(ctx, types.NamespacedName{Namespace: ns, Name: name}, pod); err != nil {
		return nil, err
	}
	return pod, nil
}

// parseQuantity wraps resource.ParseQuantity.
func parseQuantity(s string) (resource.Quantity, error) { return resource.ParseQuantity(s) }
