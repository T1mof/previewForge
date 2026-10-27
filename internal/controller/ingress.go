package controller

import (
	"context"

	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	platformv1 "github.com/T1mof/previewForge/api/v1alpha1"
)

func configureIngress(ingress *networkingv1.Ingress, spec *platformv1.IngressSpec) {
	pathType := networkingv1.PathTypePrefix
	ingress.Spec = networkingv1.IngressSpec{
		IngressClassName: &spec.ClassName,
		Rules: []networkingv1.IngressRule{{
			Host: spec.Host,
			IngressRuleValue: networkingv1.IngressRuleValue{HTTP: &networkingv1.HTTPIngressRuleValue{
				Paths: []networkingv1.HTTPIngressPath{{Path: "/", PathType: &pathType,
					Backend: networkingv1.IngressBackend{Service: &networkingv1.IngressServiceBackend{
						Name: AppName, Port: networkingv1.ServiceBackendPort{Name: "http"},
					}},
				}},
			}},
		}},
	}
	if spec.TLSSecretName != "" {
		ingress.Spec.TLS = []networkingv1.IngressTLS{{Hosts: []string{spec.Host}, SecretName: spec.TLSSecretName}}
	}
}

// reconcileIngress uses a fresh read and delete preconditions when disabling a
// route. A foreign object is never deleted, even when it uses our reserved name.
func (r *PreviewEnvironmentReconciler) reconcileIngress(ctx context.Context, p *platformv1.PreviewEnvironment, namespace string, spec platformv1.PreviewEnvironmentSpec) (bool, error) {
	ingress := &networkingv1.Ingress{ObjectMeta: metav1.ObjectMeta{Name: AppName, Namespace: namespace}}
	if spec.Ingress == nil {
		err := r.reader().Get(ctx, types.NamespacedName{Name: AppName, Namespace: namespace}, ingress)
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		if !ownedBy(ingress, p) {
			return false, nil
		}
		if ingress.DeletionTimestamp.IsZero() {
			if err := r.Delete(ctx, ingress, deletePreconditions(ingress)); err != nil && !apierrors.IsNotFound(err) {
				return false, err
			}
		}
		return true, nil
	}
	if err := r.ensure(ctx, p, ingress, func() { configureIngress(ingress, spec.Ingress) }); err != nil {
		return false, err
	}
	if !ingress.DeletionTimestamp.IsZero() {
		return true, nil
	}
	scheme := "http"
	if spec.Ingress.TLSSecretName != "" {
		scheme = "https"
	}
	p.Status.URL = scheme + "://" + spec.Ingress.Host
	return false, nil
}
