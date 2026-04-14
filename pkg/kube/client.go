package kube

import (
	"context"
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	intstr "k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Client wraps a controller-runtime client with helper methods.
type Client struct {
	Client    client.Client
	Namespace string
}

func (c *Client) EnsureDeployment(ctx context.Context, dep *appsv1.Deployment) error {
	if dep == nil {
		return fmt.Errorf("deployment is nil")
	}
	cur := &appsv1.Deployment{}
	key := types.NamespacedName{Name: dep.Name, Namespace: dep.Namespace}
	if err := c.Client.Get(ctx, key, cur); err != nil {
		// create
		return c.Client.Create(ctx, dep)
	}
	dep.ResourceVersion = cur.ResourceVersion
	return c.Client.Update(ctx, dep)
}

func (c *Client) EnsureService(ctx context.Context, svc *corev1.Service) error {
	if svc == nil {
		return fmt.Errorf("service is nil")
	}
	cur := &corev1.Service{}
	key := types.NamespacedName{Name: svc.Name, Namespace: svc.Namespace}
	if err := c.Client.Get(ctx, key, cur); err != nil {
		return c.Client.Create(ctx, svc)
	}
	// Preserve clusterIP on update
	svc.ResourceVersion = cur.ResourceVersion
	if cur.Spec.ClusterIP != "" {
		svc.Spec.ClusterIP = cur.Spec.ClusterIP
	}
	return c.Client.Update(ctx, svc)
}

// NewService constructs a basic ClusterIP service with a single port.
func NewService(ns, name string, port int32, selector map[string]string) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name},
		Spec: corev1.ServiceSpec{
			Type:     corev1.ServiceTypeClusterIP,
			Selector: selector,
			Ports: []corev1.ServicePort{{
				Name:       "http",
				Port:       port,
				TargetPort: intstrFrom(port),
			}},
		},
	}
}

func intstrFrom(p int32) intstr.IntOrString { return intstr.FromInt(int(p)) }
