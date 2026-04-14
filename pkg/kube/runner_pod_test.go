package kube

import (
	"context"
	"testing"

	sdkk8s "github.com/bubustack/bubu-sdk-go/k8s"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrlclientfake "sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestBuildEnvSourcesUsesExistingSecretWhenEphemeralDisabled(t *testing.T) {
	spec := RunnerPodSpec{
		Namespace:             "default",
		Name:                  "runner",
		ExistingEnvSecretName: "bound-secret",
		UseEphemeralSecret:    false,
		Env: map[string]string{
			"TOKEN": "should-not-be-inline",
		},
	}

	ephemeralSecretName, envFrom, envVars, err := buildEnvSources(context.Background(), nil, spec, nil)
	if err != nil {
		t.Fatalf("buildEnvSources returned error: %v", err)
	}
	if ephemeralSecretName != "" {
		t.Fatalf("expected no ephemeral secret, got %q", ephemeralSecretName)
	}
	if len(envVars) != 0 {
		t.Fatalf("expected no explicit env vars when existing secret is attached, got %#v", envVars)
	}
	if len(envFrom) != 1 || envFrom[0].SecretRef == nil || envFrom[0].SecretRef.Name != "bound-secret" {
		t.Fatalf("expected envFrom to reference bound-secret, got %#v", envFrom)
	}
}

func TestBuildEnvSourcesCreatesEphemeralSecretWhenRequested(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add corev1 to scheme: %v", err)
	}
	k := &sdkk8s.Client{
		Client: ctrlclientfake.NewClientBuilder().WithScheme(scheme).Build(),
	}

	spec := RunnerPodSpec{
		Namespace:          "default",
		Name:               "runner",
		UseEphemeralSecret: true,
		Env: map[string]string{
			"TOKEN": "token-value",
		},
	}
	ownerRef := &metav1.OwnerReference{
		APIVersion: "v1",
		Kind:       "Pod",
		Name:       "owner-pod",
	}

	ephemeralSecretName, envFrom, envVars, err := buildEnvSources(context.Background(), k, spec, ownerRef)
	if err != nil {
		t.Fatalf("buildEnvSources returned error: %v", err)
	}
	if ephemeralSecretName != "runner-env" {
		t.Fatalf("expected ephemeral secret runner-env, got %q", ephemeralSecretName)
	}
	if len(envVars) != 0 {
		t.Fatalf("expected no inline env vars when ephemeral secret is enabled, got %#v", envVars)
	}
	if len(envFrom) != 1 || envFrom[0].SecretRef == nil || envFrom[0].SecretRef.Name != "runner-env" {
		t.Fatalf("expected envFrom to reference runner-env, got %#v", envFrom)
	}

	created := &corev1.Secret{}
	key := types.NamespacedName{Namespace: "default", Name: "runner-env"}
	if err := k.Get(context.Background(), key, created); err != nil {
		t.Fatalf("expected ephemeral secret to be created: %v", err)
	}
	if string(created.Data["TOKEN"]) != "token-value" {
		t.Fatalf("expected TOKEN key in ephemeral secret, got %#v", created.Data)
	}
}
