package admission

import (
	"context"
	"testing"

	platformv1 "github.com/T1mof/previewForge/api/v1alpha1"
	coreadmission "k8s.io/api/admission/v1"
	authenticationv1 "k8s.io/api/authentication/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	webadmission "sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

func TestOwnershipIdentityConfiguration(t *testing.T) {
	reader := fake.NewClientBuilder().Build()
	naming := func(*platformv1.PreviewEnvironment) string { return "unused" }
	for _, identity := range []string{"", "system:masters", "system:serviceaccount::operator", "system:serviceaccount:bad.ns:operator", "system:serviceaccount:Valid:operator", "system:serviceaccount:valid:op:extra", "system:serviceaccount:valid:op'"} {
		if _, err := NewOwnership(reader, naming, identity); err == nil {
			t.Errorf("accepted invalid identity %q", identity)
		}
	}
	const custom = "system:serviceaccount:custom-system:previewforge-controller-manager"
	h, err := NewOwnership(reader, naming, custom)
	if err != nil {
		t.Fatal(err)
	}
	request := webadmission.Request{AdmissionRequest: coreadmission.AdmissionRequest{UserInfo: authenticationv1.UserInfo{Username: OperatorIdentity}, Object: runtime.RawExtension{Raw: []byte(`{"apiVersion":"v1","kind":"Namespace","metadata":{"name":"foreign"}}`)}}}
	if !h.Handle(context.Background(), request).Allowed {
		t.Fatal("default identity should be outside a custom installation's match")
	}
	request.UserInfo.Username = custom
	if response := h.Handle(context.Background(), request); response.Allowed || response.Result.Code != 403 {
		t.Fatalf("custom identity bypassed ownership: %+v", response)
	}
	if response := (&Ownership{}).Handle(context.Background(), request); response.Allowed || response.Result.Code != 500 {
		t.Fatalf("unconfigured handler must fail closed: %+v", response)
	}
	if _, err := NewOwnership(nil, naming, custom); err == nil {
		t.Fatal("nil reader accepted")
	}
	if _, err := NewOwnership(reader, nil, custom); err == nil {
		t.Fatal("nil naming accepted")
	}
}
