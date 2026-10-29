package controller

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/validation"

	platformv1 "github.com/T1mof/previewForge/api/v1alpha1"
)

const (
	IsolationPolicyName = "previewforge-isolation"
	GatewayPolicyName   = "previewforge-ingress"
)

// NetworkConfig is administrator configuration, never supplied by a preview CR.
// Namespace and Pod selectors are combined in one peer (AND, not OR).
type NetworkConfig struct {
	DNSNamespace     string
	DNSPods          *metav1.LabelSelector
	GatewayNamespace string
	GatewayPods      *metav1.LabelSelector
}

func NewNetworkConfig(dnsNamespace, dnsSelector, gatewayNamespace, gatewaySelector string) (NetworkConfig, error) {
	var c NetworkConfig
	if len(validation.IsDNS1123Label(dnsNamespace)) != 0 || dnsSelector == "" {
		return c, fmt.Errorf("DNS requires a namespace and a non-empty Pod selector")
	}
	var err error
	c.DNSNamespace = dnsNamespace
	if c.DNSPods, err = metav1.ParseToLabelSelector(dnsSelector); err != nil {
		return c, fmt.Errorf("DNS Pod selector: %w", err)
	}
	if len(c.DNSPods.MatchLabels)+len(c.DNSPods.MatchExpressions) == 0 {
		return c, fmt.Errorf("DNS Pod selector must not select all Pods")
	}
	if (gatewayNamespace == "") != (gatewaySelector == "") {
		return c, fmt.Errorf("Ingress controller namespace and Pod selector must be configured together")
	}
	if gatewayNamespace != "" {
		if len(validation.IsDNS1123Label(gatewayNamespace)) != 0 {
			return c, fmt.Errorf("invalid Ingress controller namespace")
		}
		c.GatewayNamespace = gatewayNamespace
		if c.GatewayPods, err = metav1.ParseToLabelSelector(gatewaySelector); err != nil {
			return c, fmt.Errorf("Ingress controller Pod selector: %w", err)
		}
		if len(c.GatewayPods.MatchLabels)+len(c.GatewayPods.MatchExpressions) == 0 {
			return c, fmt.Errorf("Ingress controller Pod selector must not select all Pods")
		}
	}
	return c, nil
}

func (c NetworkConfig) dnsPeer() networkingv1.NetworkPolicyPeer {
	if c.DNSNamespace == "" {
		c.DNSNamespace = "kube-system"
	}
	if c.DNSPods == nil {
		c.DNSPods = &metav1.LabelSelector{MatchLabels: map[string]string{"k8s-app": "kube-dns"}}
	}
	return namespacedPeer(c.DNSNamespace, c.DNSPods)
}

func namespacedPeer(namespace string, pods *metav1.LabelSelector) networkingv1.NetworkPolicyPeer {
	return networkingv1.NetworkPolicyPeer{
		NamespaceSelector: &metav1.LabelSelector{MatchLabels: map[string]string{corev1.LabelMetadataName: namespace}},
		PodSelector:       pods.DeepCopy(),
	}
}

func configureIsolation(policy *networkingv1.NetworkPolicy, config NetworkConfig) {
	tcp, udp := corev1.ProtocolTCP, corev1.ProtocolUDP
	dnsPort := intstr.FromInt32(53)
	local := networkingv1.NetworkPolicyPeer{PodSelector: &metav1.LabelSelector{}}
	policy.Spec = networkingv1.NetworkPolicySpec{
		PodSelector: metav1.LabelSelector{},
		PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeIngress, networkingv1.PolicyTypeEgress},
		Ingress:     []networkingv1.NetworkPolicyIngressRule{{From: []networkingv1.NetworkPolicyPeer{local}}},
		Egress: []networkingv1.NetworkPolicyEgressRule{
			{To: []networkingv1.NetworkPolicyPeer{local}},
			{To: []networkingv1.NetworkPolicyPeer{config.dnsPeer()}, Ports: []networkingv1.NetworkPolicyPort{{Protocol: &udp, Port: &dnsPort}, {Protocol: &tcp, Port: &dnsPort}}},
		},
	}
}

func configureGateway(policy *networkingv1.NetworkPolicy, p *platformv1.PreviewEnvironment, spec platformv1.PreviewEnvironmentSpec, config NetworkConfig) {
	policy.Spec = networkingv1.NetworkPolicySpec{PodSelector: metav1.LabelSelector{MatchLabels: selector(p)}, PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeIngress}}
	if spec.Ingress == nil {
		return
	}
	tcp, port := corev1.ProtocolTCP, intstr.FromInt32(spec.Port)
	policy.Spec.Ingress = []networkingv1.NetworkPolicyIngressRule{{
		From:  []networkingv1.NetworkPolicyPeer{namespacedPeer(config.GatewayNamespace, config.GatewayPods)},
		Ports: []networkingv1.NetworkPolicyPort{{Protocol: &tcp, Port: &port}},
	}}
}

func (r *PreviewEnvironmentReconciler) reconcileNetwork(ctx context.Context, p *platformv1.PreviewEnvironment, namespace string, spec platformv1.PreviewEnvironmentSpec) (bool, error) {
	missingGateway := spec.Ingress != nil && (r.Network.GatewayNamespace == "" || r.Network.GatewayPods == nil)
	isolation := &networkingv1.NetworkPolicy{ObjectMeta: metav1.ObjectMeta{Name: IsolationPolicyName, Namespace: namespace}}
	if err := r.ensure(ctx, p, isolation, func() { configureIsolation(isolation, r.Network) }); err != nil {
		return false, err
	}
	gateway := &networkingv1.NetworkPolicy{ObjectMeta: metav1.ObjectMeta{Name: GatewayPolicyName, Namespace: namespace}}
	if missingGateway {
		spec.Ingress = nil
	}
	if err := r.ensure(ctx, p, gateway, func() { configureGateway(gateway, p, spec, r.Network) }); err != nil {
		return false, err
	}
	if missingGateway {
		return false, fmt.Errorf("Ingress requires operator --ingress-controller-namespace and --ingress-controller-pod-selector")
	}
	return !isolation.DeletionTimestamp.IsZero() || !gateway.DeletionTimestamp.IsZero(), nil
}
