package controller

import (
	"context"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	platformv1 "github.com/T1mof/previewForge/api/v1alpha1"
)

func testNetworkConfig() NetworkConfig {
	c, err := NewNetworkConfig("kube-system", "k8s-app=kube-dns", "previewforge-ingress-system", "app.kubernetes.io/name=traefik")
	if err != nil {
		panic(err)
	}
	return c
}

func TestNetworkConfigurationRejectsUnboundedPeers(t *testing.T) {
	for _, values := range [][4]string{
		{"", "k8s-app=kube-dns", "", ""}, {"kube-system", "", "", ""},
		{"kube-system", " ", "", ""}, {"kube-system", "a=(", "", ""},
		{"kube-system", "k8s-app=kube-dns", "gateway", ""},
		{"kube-system", "k8s-app=kube-dns", "", "app=proxy"},
		{"kube-system", "k8s-app=kube-dns", "gateway", " "},
		{"kube-system", "k8s-app=kube-dns", "Bad Namespace", "app=proxy"},
		{"kube-system", "k8s-app=kube-dns", "gateway", "app=("},
	} {
		if _, err := NewNetworkConfig(values[0], values[1], values[2], values[3]); err == nil {
			t.Fatalf("unsafe peers accepted: %q", values)
		}
	}
	if _, err := NewNetworkConfig("kube-system", "k8s-app in (kube-dns)", "", ""); err != nil {
		t.Fatal(err)
	}
}

func TestNetworkPolicyOwnershipDriftAndGatewayRevocation(t *testing.T) {
	ctx := context.Background()
	p := preview("network")
	p.Spec.Ingress = &platformv1.IngressSpec{Host: "network.example.test", ClassName: "traefik"}
	r, c := fixture(t, p)
	reconcile(t, r, p.Name)
	reconcile(t, r, p.Name)
	q := &networkingv1.NetworkPolicy{}
	key := client.ObjectKey{Name: IsolationPolicyName, Namespace: NamespaceName(p)}
	if err := c.Get(ctx, key, q); err != nil {
		t.Fatal(err)
	}
	if !ownedBy(q, p) || len(q.Spec.PolicyTypes) != 2 || len(q.Spec.PodSelector.MatchLabels) != 0 {
		t.Fatal("isolation must cover the entire owned namespace")
	}
	dns := q.Spec.Egress[1]
	if len(dns.To) != 1 || dns.To[0].NamespaceSelector.MatchLabels[corev1.LabelMetadataName] != "kube-system" || dns.To[0].PodSelector.MatchLabels["k8s-app"] != "kube-dns" || len(dns.Ports) != 2 || dns.Ports[0].Port.IntVal != 53 || *dns.Ports[0].Protocol != corev1.ProtocolUDP || *dns.Ports[1].Protocol != corev1.ProtocolTCP {
		t.Fatal("DNS namespace/Pod AND selector or protocol ports missing")
	}
	q.Spec = networkingv1.NetworkPolicySpec{PodSelector: metav1.LabelSelector{}, Ingress: []networkingv1.NetworkPolicyIngressRule{{}}, Egress: []networkingv1.NetworkPolicyEgressRule{{}}}
	if err := c.Update(ctx, q); err != nil {
		t.Fatal(err)
	}
	reconcile(t, r, p.Name)
	if err := c.Get(ctx, key, q); err != nil {
		t.Fatal(err)
	}
	if len(q.Spec.Ingress[0].From) != 1 || q.Spec.Ingress[0].From[0].PodSelector == nil || len(q.Spec.Egress[0].To) != 1 {
		t.Fatal("allow-all drift survived")
	}
	gateway := &networkingv1.NetworkPolicy{}
	gwKey := client.ObjectKey{Name: GatewayPolicyName, Namespace: key.Namespace}
	if err := c.Get(ctx, gwKey, gateway); err != nil {
		t.Fatal(err)
	}
	peer := gateway.Spec.Ingress[0].From
	if len(peer) != 1 || peer[0].NamespaceSelector.MatchLabels[corev1.LabelMetadataName] != "previewforge-ingress-system" || peer[0].PodSelector.MatchLabels["app.kubernetes.io/name"] != "traefik" || gateway.Spec.PodSelector.MatchLabels[OwnerUIDLabel] != string(p.UID) {
		t.Fatal("gateway must require BOTH trusted Namespace and Pod identity")
	}
	if err := c.Delete(ctx, q); err != nil {
		t.Fatal(err)
	}
	reconcile(t, r, p.Name)
	if err := c.Get(ctx, key, q); err != nil || !ownedBy(q, p) {
		t.Fatalf("policy not recreated: %v", err)
	}
	if err := c.Get(ctx, client.ObjectKeyFromObject(p), p); err != nil {
		t.Fatal(err)
	}
	p.Spec.Port = 8081
	if err := c.Update(ctx, p); err != nil {
		t.Fatal(err)
	}
	reconcile(t, r, p.Name)
	if err := c.Get(ctx, gwKey, gateway); err != nil {
		t.Fatal(err)
	}
	if gateway.Spec.Ingress[0].Ports[0].Port.IntVal != 8081 {
		t.Fatal("gateway port did not follow spec")
	}
	if err := c.Get(ctx, client.ObjectKeyFromObject(p), p); err != nil {
		t.Fatal(err)
	}
	p.Spec.Ingress = nil
	if err := c.Update(ctx, p); err != nil {
		t.Fatal(err)
	}
	reconcile(t, r, p.Name)
	if err := c.Get(ctx, gwKey, gateway); err != nil {
		t.Fatal(err)
	}
	if len(gateway.Spec.Ingress) != 0 || len(gateway.Spec.PolicyTypes) != 1 {
		t.Fatal("disabled Ingress retained gateway access")
	}
	version := gateway.ResourceVersion
	reconcile(t, r, p.Name)
	if err := c.Get(ctx, gwKey, gateway); err != nil || gateway.ResourceVersion != version {
		t.Fatalf("idempotent policy rewritten: %v", err)
	}
}

func TestForeignNetworkPolicyBlocksDeployment(t *testing.T) {
	for _, name := range []string{IsolationPolicyName, GatewayPolicyName} {
		t.Run(name, func(t *testing.T) {
			p := preview("foreign-network")
			foreign := &networkingv1.NetworkPolicy{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: NamespaceName(p), UID: "foreign-policy", Labels: map[string]string{ManagedByLabel: "previewforge", OwnerUIDLabel: string(p.UID)}}}
			r, c := fixture(t, p, foreign)
			_ = c.Get(context.Background(), client.ObjectKeyFromObject(foreign), foreign)
			version := foreign.ResourceVersion
			reconcile(t, r, p.Name)
			_, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKey{Name: p.Name}})
			if err == nil || !strings.Contains(err.Error(), "ownership conflict") {
				t.Fatalf("foreign policy adopted: %v", err)
			}
			if err := c.Get(context.Background(), client.ObjectKeyFromObject(foreign), foreign); err != nil || foreign.ResourceVersion != version || len(foreign.OwnerReferences) != 0 {
				t.Fatalf("foreign policy mutated: %v", err)
			}
			if err := c.Get(context.Background(), client.ObjectKey{Name: AppName, Namespace: NamespaceName(p)}, &appsv1.Deployment{}); !apierrors.IsNotFound(err) {
				t.Fatalf("app created before policies: %v", err)
			}
		})
	}
}

func TestIngressRequiresAdministratorPeerConfiguration(t *testing.T) {
	p := preview("missing-gateway")
	p.Spec.Ingress = &platformv1.IngressSpec{Host: "test.example.test", ClassName: "traefik"}
	r, c := fixture(t, p)
	r.Network = NetworkConfig{}
	reconcile(t, r, p.Name)
	_, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKey{Name: p.Name}})
	if err == nil || !strings.Contains(err.Error(), "--ingress-controller-namespace") {
		t.Fatalf("unconfigured gateway allowed: %v", err)
	}
	if err := c.Get(context.Background(), client.ObjectKey{Name: AppName, Namespace: NamespaceName(p)}, &appsv1.Deployment{}); !apierrors.IsNotFound(err) {
		t.Fatalf("app started without valid network config: %v", err)
	}
}
