package controller

import (
	"context"
	"errors"
	"fmt"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	platformv1 "github.com/T1mof/previewForge/api/v1alpha1"
)

const pollInterval = 30 * time.Second

type PreviewEnvironmentReconciler struct {
	client.Client
	// APIReader bypasses informer cache for destructive operations.
	APIReader client.Reader
	Scheme    *runtime.Scheme
	Now       func() time.Time
}

// +kubebuilder:rbac:groups=platform.previewforge.dev,resources=previewenvironments,verbs=get;list;watch;update;patch;delete
// +kubebuilder:rbac:groups=platform.previewforge.dev,resources=previewenvironments/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=platform.previewforge.dev,resources=previewenvironments/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=namespaces,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=services,verbs=get;list;watch;create;update;patch
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch

func (r *PreviewEnvironmentReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	p := &platformv1.PreviewEnvironment{}
	if err := r.Get(ctx, req.NamespacedName, p); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if !p.DeletionTimestamp.IsZero() {
		return r.cleanup(ctx, p)
	}
	if p.UID == "" {
		return ctrl.Result{}, fmt.Errorf("PreviewEnvironment %s has no UID", p.Name)
	}
	if !controllerutil.ContainsFinalizer(p, Finalizer) {
		before := p.DeepCopy()
		controllerutil.AddFinalizer(p, Finalizer)
		if err := r.Patch(ctx, p, client.MergeFromWithOptions(before, client.MergeFromWithOptimisticLock{})); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: time.Millisecond}, nil
	}
	spec, err := normalizedSpec(p.Spec)
	if err != nil {
		return r.failure(ctx, p, "InvalidSpec", err)
	}
	deadline := metav1.NewTime(p.CreationTimestamp.Add(time.Duration(spec.TTLSeconds) * time.Second))
	p.Status.Namespace = NamespaceName(p)
	p.Status.ExpiresAt = &deadline
	p.Status.ServiceEndpoint = fmt.Sprintf("%s.%s.svc.cluster.local:%d", AppName, p.Status.Namespace, spec.Port)
	if !r.now().Before(deadline.Time) {
		if err := r.updateStatus(ctx, p, platformv1.PhaseExpired, "TTLExpired", "TTL elapsed; deleting the environment and its namespace", 0); err != nil {
			return ctrl.Result{}, err
		}
		// Resource version + UID prevent deleting a concurrently updated or recreated CR.
		err := r.Delete(ctx, p, deletePreconditions(p))
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if p.Status.Phase == "" {
		if err := r.updateStatus(ctx, p, platformv1.PhasePending, "Provisioning", "creating isolated preview resources", 0); err != nil {
			return ctrl.Result{}, err
		}
	}
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: p.Status.Namespace}}
	if err := r.ensure(ctx, p, ns, func() { setLabels(ns, p) }); err != nil {
		return r.failure(ctx, p, "ProvisioningFailed", err)
	}
	if !ns.DeletionTimestamp.IsZero() {
		if err := r.updateStatus(ctx, p, platformv1.PhasePending, "NamespaceTerminating", "waiting for namespace deletion before recreating resources", 0); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: r.nextPoll(deadline.Time)}, nil
	}
	d := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: AppName, Namespace: ns.Name}}
	if err := r.ensure(ctx, p, d, func() { configureDeployment(d, p, spec) }); err != nil {
		return r.failure(ctx, p, "ProvisioningFailed", err)
	}
	s := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: AppName, Namespace: ns.Name}}
	if err := r.ensure(ctx, p, s, func() { configureService(s, p, spec) }); err != nil {
		return r.failure(ctx, p, "ProvisioningFailed", err)
	}
	phase, reason, message := readiness(d, *spec.Replicas)
	if !d.DeletionTimestamp.IsZero() || !s.DeletionTimestamp.IsZero() {
		phase, reason, message = platformv1.PhaseDeploying, "ResourceTerminating", "waiting for managed resources to finish deletion"
	}
	if err := r.updateStatus(ctx, p, phase, reason, message, d.Status.ReadyReplicas); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{RequeueAfter: r.nextPoll(deadline.Time)}, nil
}

func (r *PreviewEnvironmentReconciler) ensure(ctx context.Context, p *platformv1.PreviewEnvironment, obj client.Object, configure func()) error {
	err := r.Get(ctx, client.ObjectKeyFromObject(obj), obj)
	if apierrors.IsNotFound(err) {
		if err := r.attachOwner(obj, p); err != nil {
			return err
		}
		configure()
		return r.Create(ctx, obj)
	}
	if err != nil {
		return err
	}
	if !ownedBy(obj, p) {
		return fmt.Errorf("ownership conflict: %T %s is not controlled by PreviewEnvironment UID %s", obj, client.ObjectKeyFromObject(obj), p.UID)
	}
	if !obj.GetDeletionTimestamp().IsZero() {
		return nil
	}
	before := obj.DeepCopyObject().(client.Object)
	setLabels(obj, p)
	configure()
	if equality.Semantic.DeepEqual(before, obj) {
		return nil
	}
	// Include resourceVersion so stale cache reads cannot overwrite a concurrent change.
	return r.Patch(ctx, obj, client.MergeFromWithOptions(before, client.MergeFromWithOptimisticLock{}))
}

func (r *PreviewEnvironmentReconciler) updateStatus(ctx context.Context, p *platformv1.PreviewEnvironment, phase platformv1.Phase, reason, message string, ready int32) error {
	// Fetch the current object to preserve finalizers and detect spec changes.
	current := &platformv1.PreviewEnvironment{}
	if err := r.reader().Get(ctx, client.ObjectKeyFromObject(p), current); err != nil {
		return err
	}
	if current.UID != p.UID || current.Generation != p.Generation || !current.DeletionTimestamp.IsZero() {
		return apierrors.NewConflict(platformv1.GroupVersion.WithResource("previewenvironments").GroupResource(), p.Name, fmt.Errorf("resource changed during reconciliation"))
	}
	before := current.DeepCopy()
	// Keep conditions from the uncached read. A cached snapshot at the same
	// generation can still miss a newer condition or Ready transition timestamp.
	current.Status.Namespace = p.Status.Namespace
	current.Status.ServiceEndpoint = p.Status.ServiceEndpoint
	current.Status.ExpiresAt = p.Status.ExpiresAt.DeepCopy()
	current.Status.Phase = phase
	current.Status.Message = message
	current.Status.ReadyReplicas = ready
	current.Status.ObservedGeneration = p.Generation
	conditionStatus := metav1.ConditionFalse
	if phase == platformv1.PhaseReady {
		conditionStatus = metav1.ConditionTrue
	}
	meta.SetStatusCondition(&current.Status.Conditions, metav1.Condition{
		Type: "Ready", Status: conditionStatus, Reason: reason, Message: message,
		ObservedGeneration: p.Generation, LastTransitionTime: metav1.NewTime(r.now()),
	})
	if !equality.Semantic.DeepEqual(before.Status, current.Status) {
		if err := r.Status().Patch(ctx, current, client.MergeFromWithOptions(before, client.MergeFromWithOptimisticLock{})); err != nil {
			return err
		}
	}
	// Keep the latest resourceVersion for TTL deletion preconditions.
	*p = *current
	return nil
}

func (r *PreviewEnvironmentReconciler) failure(ctx context.Context, p *platformv1.PreviewEnvironment, reason string, cause error) (ctrl.Result, error) {
	statusErr := r.updateStatus(ctx, p, platformv1.PhaseFailed, reason, cause.Error(), 0)
	return ctrl.Result{}, errors.Join(cause, statusErr)
}

func (r *PreviewEnvironmentReconciler) cleanup(ctx context.Context, p *platformv1.PreviewEnvironment) (ctrl.Result, error) {
	if !controllerutil.ContainsFinalizer(p, Finalizer) {
		return ctrl.Result{}, nil
	}
	// Never trust status.namespace or labels as authorization to delete resources.
	ns := &corev1.Namespace{}
	err := r.reader().Get(ctx, types.NamespacedName{Name: NamespaceName(p)}, ns)
	if apierrors.IsNotFound(err) {
		before := p.DeepCopy()
		controllerutil.RemoveFinalizer(p, Finalizer)
		return ctrl.Result{}, r.Patch(ctx, p, client.MergeFromWithOptions(before, client.MergeFromWithOptimisticLock{}))
	}
	if err != nil {
		return ctrl.Result{}, err
	}
	if !ownedBy(ns, p) {
		return ctrl.Result{}, fmt.Errorf("refusing to delete namespace %s: ownership conflict", ns.Name)
	}
	if ns.DeletionTimestamp.IsZero() {
		if err := r.Delete(ctx, ns, deletePreconditions(ns)); err != nil && !apierrors.IsNotFound(err) {
			return ctrl.Result{}, err
		}
	}
	// Namespace controller removes all namespaced resources; keep finalizer until it is gone.
	return ctrl.Result{RequeueAfter: 2 * time.Second}, nil
}

func deletePreconditions(obj client.Object) *client.DeleteOptions {
	uid, rv := obj.GetUID(), obj.GetResourceVersion()
	return &client.DeleteOptions{Preconditions: &metav1.Preconditions{UID: &uid, ResourceVersion: &rv}}
}

func (r *PreviewEnvironmentReconciler) reader() client.Reader {
	if r.APIReader != nil {
		return r.APIReader
	}
	return r.Client
}

func (r *PreviewEnvironmentReconciler) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

func (r *PreviewEnvironmentReconciler) nextPoll(deadline time.Time) time.Duration {
	remaining := deadline.Sub(r.now())
	if remaining < time.Millisecond {
		return time.Millisecond
	}
	if remaining < pollInterval {
		return remaining
	}
	return pollInterval
}

func (r *PreviewEnvironmentReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&platformv1.PreviewEnvironment{}).
		Owns(&corev1.Namespace{}).
		Owns(&appsv1.Deployment{}).
		Owns(&corev1.Service{}).
		WithOptions(controller.Options{MaxConcurrentReconciles: 4}).
		Complete(r)
}
