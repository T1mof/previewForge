// Package admission implements live ownership checks that CEL cannot perform.
package admission

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"
	webadmission "sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	platformv1 "github.com/T1mof/previewForge/api/v1alpha1"
)

const OperatorIdentity = "system:serviceaccount:previewforge-system:previewforge-controller-manager"
const WebhookName = "ownership.platform.previewforge.dev"
const Path = "/validate-ownership"

// Ownership uses an uncached API reader. Its ServiceAccount has only get access.
type Ownership struct {
	Reader        client.Reader
	NamespaceName func(*platformv1.PreviewEnvironment) string
}

func owner(obj metav1.Object) (*metav1.OwnerReference, error) {
	r := metav1.GetControllerOf(obj)
	if r == nil || r.APIVersion != platformv1.GroupVersion.String() || r.Kind != "PreviewEnvironment" || r.Name == "" || r.UID == "" {
		return nil, fmt.Errorf("PreviewEnvironment controller API version/kind/name/UID required")
	}
	return r, nil
}

func (h *Ownership) Handle(ctx context.Context, req webadmission.Request) webadmission.Response {
	if req.UserInfo.Username != OperatorIdentity {
		return webadmission.Allowed("identity outside operator boundary")
	}
	if req.SubResource != "" {
		return webadmission.Denied("PreviewForge ownership: unexpected subresource")
	}
	raw := req.Object.Raw
	if req.Operation == admissionv1.Delete {
		raw = req.OldObject.Raw
	}
	obj := &unstructured.Unstructured{}
	if err := json.Unmarshal(raw, obj); err != nil {
		return webadmission.Errored(http.StatusBadRequest, err)
	}
	r, err := owner(obj)
	if err != nil {
		return webadmission.Denied("PreviewForge ownership: " + err.Error())
	}
	if req.Operation != admissionv1.Create {
		old := &unstructured.Unstructured{}
		if err := json.Unmarshal(req.OldObject.Raw, old); err != nil {
			return webadmission.Errored(http.StatusBadRequest, err)
		}
		previous, err := owner(old)
		if err != nil || previous.UID != r.UID || previous.Name != r.Name {
			return webadmission.Denied("PreviewForge ownership: foreign resources cannot be adopted or re-owned")
		}
	}
	p := &platformv1.PreviewEnvironment{}
	if err := h.Reader.Get(ctx, client.ObjectKey{Name: r.Name}, p); err != nil {
		return webadmission.Denied("PreviewForge ownership: live PreviewEnvironment unavailable")
	}
	if p.UID != r.UID {
		return webadmission.Denied("PreviewForge ownership: live PreviewEnvironment UID differs")
	}
	if h.NamespaceName == nil {
		return webadmission.Errored(http.StatusInternalServerError, fmt.Errorf("namespace naming is not configured"))
	}
	want := h.NamespaceName(p)
	if req.Resource.Group == "" && req.Resource.Resource == "namespaces" {
		if obj.GetName() != want {
			return webadmission.Denied("PreviewForge ownership: Namespace name does not match live controller UID")
		}
	} else {
		if req.Namespace != want || obj.GetNamespace() != want {
			return webadmission.Denied("PreviewForge ownership: child Namespace does not match live controller UID")
		}
		ns := &corev1.Namespace{}
		if err := h.Reader.Get(ctx, client.ObjectKey{Name: req.Namespace}, ns); err != nil {
			return webadmission.Denied("PreviewForge ownership: live Namespace unavailable")
		}
		nsOwner, err := owner(ns)
		if err != nil || nsOwner.UID != r.UID || nsOwner.Name != r.Name {
			return webadmission.Denied("PreviewForge ownership: live Namespace controller name/UID differs")
		}
	}
	return webadmission.Allowed("live PreviewEnvironment and Namespace ownership verified")
}
