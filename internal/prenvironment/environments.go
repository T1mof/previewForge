// Package prenvironment manages PR-owned PreviewEnvironments without storing a
// separate environment registry. The source Namespace UID binds every operation
// to the explicitly configured cluster and repository installation.
package prenvironment

import (
	"context"
	"errors"
	"fmt"
	"net"
	"regexp"
	"strconv"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"

	platformv1 "github.com/T1mof/previewForge/api/v1alpha1"
	"github.com/T1mof/previewForge/internal/policy"
)

const (
	DefaultSourceNamespace = "previewforge-pr-source"
	RevisionAnnotation     = "platform.previewforge.dev/revision"
)

var (
	ErrForeign     = errors.New("environment is not controlled by this PR source")
	ErrBinding     = errors.New("source binding could not be verified")
	ErrReplaced    = errors.New("environment UID changed during the operation")
	ErrTerminating = errors.New("environment is terminating; wait for cleanup before applying")
	digestImage    = regexp.MustCompile(`^[^\s@]+@sha256:[a-f0-9]{64}$`)
	commitSHA      = regexp.MustCompile(`^([a-f0-9]{40}|[a-f0-9]{64})$`)
)

type Binding struct {
	Namespace string
	UID       types.UID
}

type Request struct {
	RepositoryID    int64
	Number          int64
	Image           string
	Revision        string
	Port            int32
	Replicas        int32
	TTLSeconds      int64
	Host            string
	ClassName       string
	TLSSecretName   string
	Resources       *platformv1.ComputeResources
	SecurityProfile platformv1.SecurityProfile
}

type Environments struct {
	Client client.Client
	Source Binding
}

// The deployer receives no Namespace writes, workloads, Secrets or status access.
// +kubebuilder:rbac:groups="",resources=namespaces,resourceNames=previewforge-pr-source,verbs=get
// +kubebuilder:rbac:groups=platform.previewforge.dev,resources=previewenvironments,verbs=get;create;patch;delete

func Name(repositoryID, number int64) (string, error) {
	if repositoryID <= 0 || number <= 0 {
		return "", errors.New("positive repository ID and PR number are required")
	}
	return fmt.Sprintf("pr-%d-%d", repositoryID, number), nil
}

func (r Request) spec() (platformv1.PreviewEnvironmentSpec, error) {
	if !digestImage.MatchString(r.Image) || len(r.Image) > 512 {
		return platformv1.PreviewEnvironmentSpec{}, errors.New("image must be an immutable reference ending with @sha256:<64 lowercase hex digits>")
	}
	if !commitSHA.MatchString(r.Revision) {
		return platformv1.PreviewEnvironmentSpec{}, errors.New("revision must be a full lowercase commit SHA")
	}
	if r.Port < 1 || r.Port > 65535 || r.Replicas < 1 || r.Replicas > 10 || r.TTLSeconds < 1 || r.TTLSeconds > 604800 {
		return platformv1.PreviewEnvironmentSpec{}, errors.New("port, replicas or TTL outside PreviewEnvironment API limits")
	}
	spec := platformv1.PreviewEnvironmentSpec{Image: r.Image, Port: r.Port, Replicas: &r.Replicas, TTLSeconds: r.TTLSeconds}
	var err error
	if spec.SecurityProfile, err = policy.NormalizeProfile(r.SecurityProfile); err != nil {
		return spec, err
	}
	if r.Resources != nil {
		if spec.Resources, err = policy.NormalizeResources(r.Resources); err != nil {
			return spec, err
		}
	}
	if r.Host == "" {
		if r.ClassName != "" || r.TLSSecretName != "" {
			return spec, errors.New("host is required with an ingress class or TLS Secret")
		}
		return spec, nil
	}
	if net.ParseIP(r.Host) != nil || len(validation.IsDNS1123Subdomain(r.Host)) != 0 || len(validation.IsDNS1123Subdomain(r.ClassName)) != 0 {
		return spec, errors.New("an exact DNS host and ingress class are required")
	}
	if r.TLSSecretName != "" && len(validation.IsDNS1123Subdomain(r.TLSSecretName)) != 0 {
		return spec, errors.New("invalid TLS Secret name")
	}
	spec.Ingress = &platformv1.IngressSpec{Host: r.Host, ClassName: r.ClassName, TLSSecretName: r.TLSSecretName}
	return spec, nil
}

func (e *Environments) source(ctx context.Context, allowDeleting bool) (*corev1.Namespace, error) {
	if e.Client == nil || len(validation.IsDNS1123Label(e.Source.Namespace)) != 0 || e.Source.UID == "" {
		return nil, fmt.Errorf("%w: a Kubernetes client, explicit source Namespace and expected UID are required", ErrBinding)
	}
	ns := &corev1.Namespace{}
	if err := e.Client.Get(ctx, client.ObjectKey{Name: e.Source.Namespace}, ns); err != nil {
		return nil, fmt.Errorf("%w: read source Namespace: %v", ErrBinding, err)
	}
	if ns.UID != e.Source.UID {
		return nil, fmt.Errorf("%w: source Namespace UID mismatch; refusing this cluster or replaced installation", ErrBinding)
	}
	if !allowDeleting && !ns.DeletionTimestamp.IsZero() {
		return nil, fmt.Errorf("%w: source Namespace is terminating", ErrBinding)
	}
	return ns, nil
}

func (e *Environments) owns(p *platformv1.PreviewEnvironment) bool {
	owner := metav1.GetControllerOf(p)
	return owner != nil && e.Source.UID != "" && owner.APIVersion == "v1" && owner.Kind == "Namespace" && owner.Name == e.Source.Namespace && owner.UID == e.Source.UID
}

// Apply preserves creationTimestamp (TTL does not restart) and unrelated metadata.
// Both create races and optimistic-lock conflicts re-read and recheck ownership.
func (e *Environments) Apply(ctx context.Context, request Request) (*platformv1.PreviewEnvironment, error) {
	name, err := Name(request.RepositoryID, request.Number)
	if err != nil {
		return nil, err
	}
	spec, err := request.spec()
	if err != nil {
		return nil, err
	}
	var result *platformv1.PreviewEnvironment
	var observedUID types.UID
	err = retry.OnError(retry.DefaultRetry, func(err error) bool { return apierrors.IsConflict(err) || apierrors.IsAlreadyExists(err) }, func() error {
		if _, err := e.source(ctx, false); err != nil {
			return err
		}
		p := &platformv1.PreviewEnvironment{}
		err := e.Client.Get(ctx, client.ObjectKey{Name: name}, p)
		if apierrors.IsNotFound(err) {
			if observedUID != "" {
				return ErrReplaced
			}
			controller, block := true, false
			p = &platformv1.PreviewEnvironment{ObjectMeta: metav1.ObjectMeta{Name: name, OwnerReferences: []metav1.OwnerReference{{APIVersion: "v1", Kind: "Namespace", Name: e.Source.Namespace, UID: e.Source.UID, Controller: &controller, BlockOwnerDeletion: &block}}}}
			p.Spec = spec
			setMetadata(p, request)
			if err := e.Client.Create(ctx, p); err != nil {
				return err
			}
			result = p
			return nil
		}
		if err != nil {
			return err
		}
		if !e.owns(p) {
			return fmt.Errorf("%w: %s", ErrForeign, name)
		}
		if observedUID != "" && p.UID != observedUID {
			return ErrReplaced
		}
		observedUID = p.UID
		if !p.DeletionTimestamp.IsZero() {
			return ErrTerminating
		}
		before := p.DeepCopy()
		p.Spec = spec
		setMetadata(p, request)
		if !equality.Semantic.DeepEqual(before, p) {
			if err := e.Client.Patch(ctx, p, client.MergeFromWithOptions(before, client.MergeFromWithOptimisticLock{})); err != nil {
				return err
			}
		}
		result = p
		return nil
	})
	return result, err
}

func setMetadata(p *platformv1.PreviewEnvironment, request Request) {
	if p.Annotations == nil {
		p.Annotations = map[string]string{}
	}
	p.Annotations[RevisionAnnotation] = request.Revision
	if p.Labels == nil {
		p.Labels = map[string]string{}
	}
	p.Labels["app.kubernetes.io/managed-by"] = "previewforge-pr"
	p.Labels["platform.previewforge.dev/repository-id"] = strconv.FormatInt(request.RepositoryID, 10)
	p.Labels["platform.previewforge.dev/pr-number"] = strconv.FormatInt(request.Number, 10)
}

func (e *Environments) Get(ctx context.Context, repositoryID, number int64) (*platformv1.PreviewEnvironment, error) {
	name, err := Name(repositoryID, number)
	if err != nil {
		return nil, err
	}
	if _, err := e.source(ctx, true); err != nil {
		return nil, err
	}
	p := &platformv1.PreviewEnvironment{}
	if err := e.Client.Get(ctx, client.ObjectKey{Name: name}, p); err != nil {
		return nil, err
	}
	if !e.owns(p) {
		return nil, fmt.Errorf("%w: %s", ErrForeign, name)
	}
	return p, nil
}

// Delete returns the deleted UID. A later replacement must not be deleted or
// awaited as though it were the original object. Missing environments are a no-op.
func (e *Environments) Delete(ctx context.Context, repositoryID, number int64) (types.UID, error) {
	var pinnedUID types.UID
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		p, err := e.Get(ctx, repositoryID, number)
		if apierrors.IsNotFound(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if p.UID == "" || p.ResourceVersion == "" {
			return errors.New("refusing deletion without object UID/resourceVersion")
		}
		if pinnedUID != "" && p.UID != pinnedUID {
			return ErrReplaced
		}
		pinnedUID = p.UID
		if !p.DeletionTimestamp.IsZero() {
			return nil
		}
		uid, version := p.UID, p.ResourceVersion
		err = e.Client.Delete(ctx, p, &client.DeleteOptions{Preconditions: &metav1.Preconditions{UID: &uid, ResourceVersion: &version}})
		return client.IgnoreNotFound(err)
	})
	return pinnedUID, err
}

func Ready(p *platformv1.PreviewEnvironment) bool {
	condition := meta.FindStatusCondition(p.Status.Conditions, "Ready")
	if !p.DeletionTimestamp.IsZero() || p.Status.Phase != platformv1.PhaseReady || p.Status.ObservedGeneration != p.Generation || condition == nil || condition.Status != metav1.ConditionTrue || condition.ObservedGeneration != p.Generation {
		return false
	}
	url := ""
	if p.Spec.Ingress != nil {
		scheme := "http"
		if p.Spec.Ingress.TLSSecretName != "" {
			scheme = "https"
		}
		url = scheme + "://" + p.Spec.Ingress.Host
	}
	return p.Status.URL == url
}

func (e *Environments) Deleted(ctx context.Context, repositoryID, number int64, uid types.UID) (bool, error) {
	if uid == "" {
		return false, errors.New("deleted UID is required")
	}
	name, err := Name(repositoryID, number)
	if err != nil {
		return false, err
	}
	if _, err := e.source(ctx, true); err != nil {
		return false, err
	}
	p := &platformv1.PreviewEnvironment{}
	err = e.Client.Get(ctx, client.ObjectKey{Name: name}, p)
	if apierrors.IsNotFound(err) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	if p.UID != uid {
		return true, nil
	}
	if !e.owns(p) {
		return false, ErrForeign
	}
	return false, nil
}
