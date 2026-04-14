package kube

import (
	"context"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

type EnsureSpec struct {
	EngramName                   string
	Namespace                    string
	Image                        string
	ImagePullPolicy              corev1.PullPolicy
	Env                          map[string]string
	EnvFromBuckets               []string
	Resources                    corev1.ResourceRequirements
	Annotations                  map[string]string
	Labels                       map[string]string
	NodeSelector                 map[string]string
	Tolerations                  []string
	ServiceName                  string
	ServiceType                  corev1.ServiceType
	ServicePort                  int32
	Security                     *corev1.SecurityContext
	Strategy                     *appsv1.DeploymentStrategy
	Headless                     bool
	ServiceAnnotations           map[string]string
	ServiceLabels                map[string]string
	ServiceAccountName           string
	AutomountServiceAccountToken *bool
	LivenessProbe                *corev1.Probe
	ReadinessProbe               *corev1.Probe
	StartupProbe                 *corev1.Probe
}

func (c *Client) Ensure(ctx context.Context, s EnsureSpec) error {
	labels := CommonLabels(s.EngramName)
	for k, v := range s.Labels {
		labels[k] = v
	}
	// Ensure labels include a per-instance identifier for uniqueness/routing
	if _, ok := labels["bubustack.io/engram-instance"]; !ok {
		labels["bubustack.io/engram-instance"] = s.EngramName
	}

	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:   s.Namespace,
			Name:        s.EngramName,
			Labels:      labels,
			Annotations: map[string]string{"mcp.bubustack.io/spec-hash": SpecHash(s)},
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: int32ptr(1),
			Selector: &metav1.LabelSelector{MatchLabels: labels},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels, Annotations: s.Annotations},
				Spec: corev1.PodSpec{
					ServiceAccountName:           s.ServiceAccountName,
					AutomountServiceAccountToken: s.AutomountServiceAccountToken,
					NodeSelector:                 s.NodeSelector,
					Containers: []corev1.Container{{
						Name:            "mcp-server",
						Image:           s.Image,
						ImagePullPolicy: s.ImagePullPolicy,
						Ports:           []corev1.ContainerPort{{ContainerPort: s.ServicePort}},
						Env:             toEnvVars(s.Env),
						Resources:       s.Resources,
						SecurityContext: s.Security,
						LivenessProbe:   s.LivenessProbe,
						ReadinessProbe:  s.ReadinessProbe,
						StartupProbe:    s.StartupProbe,
					}},
				},
			},
		},
	}

	if s.Strategy != nil {
		dep.Spec.Strategy = *s.Strategy
	}

	if err := c.EnsureDeployment(ctx, dep); err != nil {
		return err
	}

	svcName := s.ServiceName
	if svcName == "" {
		svcName = s.EngramName
	}
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:   s.Namespace,
			Name:        svcName,
			Labels:      s.ServiceLabels,
			Annotations: s.ServiceAnnotations,
		},
		Spec: corev1.ServiceSpec{
			Type:     s.ServiceType,
			Selector: labels,
			Ports: []corev1.ServicePort{{
				Name:       "http",
				Port:       s.ServicePort,
				TargetPort: intstr.FromInt(int(s.ServicePort)),
			}},
		},
	}
	if s.Headless {
		svc.Spec.ClusterIP = corev1.ClusterIPNone
	}
	return c.EnsureService(ctx, svc)
}

func toEnvVars(env map[string]string) []corev1.EnvVar {
	if len(env) == 0 {
		return nil
	}
	out := make([]corev1.EnvVar, 0, len(env))
	for k, v := range env {
		out = append(out, corev1.EnvVar{Name: k, Value: v})
	}
	return out
}

func int32ptr(i int32) *int32 { return &i }
