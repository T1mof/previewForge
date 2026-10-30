package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	platform "github.com/T1mof/previewForge/api/v1alpha1"
	"github.com/T1mof/previewForge/internal/controller"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func sameUID(object client.Object, uid types.UID) error {
	if uid == "" || object.GetUID() != uid {
		return fmt.Errorf("original UID changed for %T %s", object, object.GetName())
	}
	return nil
}

func owned(object metav1.Object, apiVersion, kind, name string, uid types.UID) bool {
	ref := metav1.GetControllerOf(object)
	return uid != "" && ref != nil && ref.APIVersion == apiVersion && ref.Kind == kind && ref.Name == name && ref.UID == uid
}

func previewOwned(object metav1.Object, f *fixture) bool {
	return owned(object, platform.GroupVersion.String(), "PreviewEnvironment", f.Name, f.UID)
}

func (b *runner) fresh(ctx context.Context, f *fixture) (*platform.PreviewEnvironment, error) {
	p := &platform.PreviewEnvironment{}
	if err := b.c.Get(ctx, client.ObjectKey{Name: f.Name}, p); err != nil {
		return nil, err
	}
	return p, sameUID(p, f.UID)
}

func (b *runner) create(ctx context.Context, f *fixture) error {
	f.start = time.Now()
	p := &platform.PreviewEnvironment{ObjectMeta: metav1.ObjectMeta{Name: f.Name, Labels: map[string]string{"e2e.previewforge.dev/run": runLabel()}}, Spec: platform.PreviewEnvironmentSpec{
		Image: b.o.Image, Replicas: new(int32(1)), Port: 8080, TTLSeconds: 3600, SecurityProfile: platform.SecurityRestricted,
		Resources: &platform.ComputeResources{Requests: platform.ResourceRequests{CPU: "10m", Memory: "16Mi"}, Limits: platform.ResourceLimits{CPU: "100m", Memory: "64Mi"}},
	}}
	// Create, never apply/adopt an existing fixture name.
	if err := b.c.Create(ctx, p); err != nil {
		return err
	}
	f.UID = p.UID
	f.Created = p.CreationTimestamp.Time
	f.Namespace = controller.NamespaceName(p)
	f.Generation = p.Generation
	f.Replicas = 1
	f.Children = map[string]types.UID{}
	return nil
}

func (b *runner) update(ctx context.Context, f *fixture, mutate func(*platform.PreviewEnvironment)) error {
	f.start = time.Now()
	return retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		p, err := b.fresh(ctx, f)
		if err != nil {
			return err
		}
		mutate(p)
		if err = b.c.Update(ctx, p); err != nil {
			return err
		}
		f.Generation = p.Generation
		f.Replicas = *p.Spec.Replicas
		return nil
	})
}

func (b *runner) setReplicas(ctx context.Context, items []*fixture, n int32) error {
	return parallel(ctx, b.o.Workers, items, func(ctx context.Context, f *fixture) error {
		return b.update(ctx, f, func(p *platform.PreviewEnvironment) { p.Spec.Replicas = new(n) })
	})
}

func wait(ctx context.Context, limit time.Duration, fn func(context.Context) (bool, error)) error {
	ctx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	for {
		ok, err := fn(ctx)
		if err != nil {
			return err
		}
		if ok {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
}

// Five batch reads per observation, rather than issuing per-Pod/per-CR polling.
// Selectors narrow discovery only; every selected object still needs UID proof.
func (b *runner) ready(ctx context.Context, items []*fixture, name string) error {
	b.phase(name)
	wave := time.Now()
	times := make(map[*fixture]float64)
	proof := make(map[*fixture]readyProof)
	err := wait(ctx, 240*time.Second, func(ctx context.Context) (bool, error) {
		previews := &platform.PreviewEnvironmentList{}
		deployments := &appsv1.DeploymentList{}
		pods := &corev1.PodList{}
		sets := &appsv1.ReplicaSetList{}
		namespaces := &corev1.NamespaceList{}
		for _, list := range []client.ObjectList{previews, deployments, pods, sets, namespaces} {
			if err := b.c.List(ctx, list); err != nil {
				return false, err
			}
		}
		pm := map[string]*platform.PreviewEnvironment{}
		dm := map[string]*appsv1.Deployment{}
		nm := map[string]*corev1.Namespace{}
		rm := map[types.UID]*appsv1.ReplicaSet{}
		for i := range previews.Items {
			p := &previews.Items[i]
			pm[p.Name] = p
		}
		for i := range deployments.Items {
			d := &deployments.Items[i]
			if d.Name == controller.AppName {
				dm[d.Namespace] = d
			}
		}
		for i := range namespaces.Items {
			n := &namespaces.Items[i]
			nm[n.Name] = n
		}
		for i := range sets.Items {
			rs := &sets.Items[i]
			rm[rs.UID] = rs
		}
		for _, f := range items {
			previousTime, wasReady := times[f]
			delete(times, f)
			p, n, d := pm[f.Name], nm[f.Namespace], dm[f.Namespace]
			if p == nil {
				return false, fmt.Errorf("fixture disappeared before readiness: %s", f.Name)
			}
			if err := sameUID(p, f.UID); err != nil {
				return false, err
			}
			if n == nil {
				continue
			}
			if !previewOwned(n, f) {
				return false, fmt.Errorf("foreign namespace: %s", f.Namespace)
			}
			if f.NamespaceUID == "" {
				f.NamespaceUID = n.UID
			} else if err := sameUID(n, f.NamespaceUID); err != nil {
				return false, err
			}
			if d == nil {
				continue
			}
			if !previewOwned(d, f) {
				return false, fmt.Errorf("foreign deployment: %s", f.Namespace)
			}
			if len(f.DeploymentUIDs) == 0 {
				f.DeploymentUIDs = append(f.DeploymentUIDs, d.UID)
			} else if d.UID != f.DeploymentUIDs[len(f.DeploymentUIDs)-1] {
				if !f.replace {
					return false, fmt.Errorf("unplanned Deployment replacement: %s", f.Name)
				}
				f.DeploymentUIDs = append(f.DeploymentUIDs, d.UID)
				f.replace = false
			}
			if f.replace {
				continue
			} // a deliberate deletion must actually produce a new UID
			readyPods := int32(0)
			var podUIDs []types.UID
			activePods := int32(0)
			for i := range pods.Items {
				pod := &pods.Items[i]
				if pod.Namespace != f.Namespace || !pod.DeletionTimestamp.IsZero() {
					continue
				}
				activePods++
				ref := metav1.GetControllerOf(pod)
				if ref == nil {
					continue
				}
				rs := rm[ref.UID]
				if rs == nil || !owned(pod, "apps/v1", "ReplicaSet", rs.Name, rs.UID) || !owned(rs, "apps/v1", "Deployment", d.Name, d.UID) {
					continue
				}
				if len(pod.Spec.Containers) != 1 || pod.Spec.Containers[0].Image != b.o.Image {
					continue
				}
				for _, condition := range pod.Status.Conditions {
					if condition.Type == corev1.PodReady && condition.Status == corev1.ConditionTrue && pod.Status.Phase == corev1.PodRunning {
						readyPods++
						podUIDs = append(podUIDs, pod.UID)
					}
				}
			}
			condition := meta.FindStatusCondition(p.Status.Conditions, "Ready")
			if p.Generation == f.Generation && p.Status.ObservedGeneration == p.Generation && p.Status.Phase == platform.PhaseReady && p.Status.ReadyReplicas == f.Replicas && p.Status.Namespace == f.Namespace && condition != nil && condition.Status == metav1.ConditionTrue && condition.ObservedGeneration == p.Generation &&
				d.Spec.Replicas != nil && *d.Spec.Replicas == f.Replicas && d.Status.ObservedGeneration == d.Generation && d.Status.ReadyReplicas == f.Replicas && d.Status.AvailableReplicas == f.Replicas && d.Status.UpdatedReplicas == f.Replicas && d.Status.Replicas == f.Replicas && readyPods == f.Replicas && activePods == f.Replicas {
				if wasReady {
					times[f] = previousTime
				} else {
					times[f] = time.Since(f.start).Seconds()
				}
				proof[f] = readyProof{Name: f.Name, CRUID: p.UID, NamespaceUID: n.UID, DeploymentUID: d.UID, Generation: p.Generation, ObservedGeneration: p.Status.ObservedGeneration, DeploymentGeneration: d.Generation, DeploymentObservedGeneration: d.Status.ObservedGeneration, Replicas: f.Replicas, PodUIDs: podUIDs}
			} else {
				delete(times, f)
			}
		}
		return len(times) == len(items), nil
	})
	if err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	values := make([]float64, 0, len(items))
	proofs := make([]readyProof, 0, len(items))
	for _, f := range items {
		values = append(values, times[f])
		proofs = append(proofs, proof[f])
		if err = b.integrity(ctx, f); err != nil {
			return err
		}
	}
	b.r.Measurements = append(b.r.Measurements, measurement{Name: name, Seconds: values, P50: quantile(values, .5), P95: quantile(values, .95), Max: quantile(values, 1), Wall: time.Since(wave).Seconds(), Proof: proofs})
	return nil
}

func (b *runner) integrity(ctx context.Context, f *fixture) error {
	for key, obj := range map[string]client.Object{
		"service":   &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: controller.AppName}},
		"quota":     &corev1.ResourceQuota{ObjectMeta: metav1.ObjectMeta{Name: controller.QuotaName}},
		"limits":    &corev1.LimitRange{ObjectMeta: metav1.ObjectMeta{Name: controller.LimitRangeName}},
		"isolation": &networkingv1.NetworkPolicy{ObjectMeta: metav1.ObjectMeta{Name: controller.IsolationPolicyName}},
		"gateway":   &networkingv1.NetworkPolicy{ObjectMeta: metav1.ObjectMeta{Name: controller.GatewayPolicyName}},
	} {
		if err := b.c.Get(ctx, client.ObjectKey{Namespace: f.Namespace, Name: obj.GetName()}, obj); err != nil {
			return err
		}
		if !previewOwned(obj, f) {
			return fmt.Errorf("foreign child %s/%s", f.Namespace, key)
		}
		if uid, ok := f.Children[key]; ok {
			if err := sameUID(obj, uid); err != nil {
				return err
			}
		} else {
			f.Children[key] = obj.GetUID()
		}
	}
	return nil
}

func deleteOriginal(ctx context.Context, c client.Client, obj client.Object, uid types.UID) error {
	if err := c.Get(ctx, client.ObjectKeyFromObject(obj), obj); apierrors.IsNotFound(err) {
		return nil
	} else if err != nil {
		return err
	}
	if err := sameUID(obj, uid); err != nil {
		return err
	}
	rv := obj.GetResourceVersion()
	return client.IgnoreNotFound(c.Delete(ctx, obj, &client.DeleteOptions{Preconditions: &metav1.Preconditions{UID: &uid, ResourceVersion: &rv}}))
}

func (b *runner) deleteDeployment(ctx context.Context, f *fixture) error {
	d := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: controller.AppName, Namespace: f.Namespace}}
	if err := b.c.Get(ctx, client.ObjectKeyFromObject(d), d); err != nil {
		return err
	}
	if !previewOwned(d, f) {
		return errors.New("refusing foreign Deployment")
	}
	if err := sameUID(d, f.DeploymentUIDs[len(f.DeploymentUIDs)-1]); err != nil {
		return err
	}
	if err := deleteOriginal(ctx, b.c, d, d.UID); err != nil {
		return err
	}
	f.replace = true
	f.start = time.Now()
	return nil
}

func (b *runner) deleteCR(ctx context.Context, f *fixture) error {
	if f.UID == "" {
		return nil
	}
	f.start = time.Now()
	return retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		return deleteOriginal(ctx, b.c, &platform.PreviewEnvironment{ObjectMeta: metav1.ObjectMeta{Name: f.Name}}, f.UID)
	})
}

func (b *runner) gone(ctx context.Context, items []*fixture, name string) error {
	b.phase(name)
	wave := time.Now()
	times := map[*fixture]float64{}
	err := wait(ctx, 180*time.Second, func(ctx context.Context) (bool, error) {
		for _, f := range items {
			if f.UID == "" || f.Removed {
				times[f] = 0
				continue
			}
			p, err := b.fresh(ctx, f)
			if err == nil {
				_ = p
				continue
			}
			if !apierrors.IsNotFound(err) {
				return false, err
			}
			if f.Namespace != "" {
				n := &corev1.Namespace{}
				err = b.c.Get(ctx, client.ObjectKey{Name: f.Namespace}, n)
				if err == nil {
					if f.NamespaceUID != "" {
						if err = sameUID(n, f.NamespaceUID); err != nil {
							return false, err
						}
					}
					continue
				}
				if !apierrors.IsNotFound(err) {
					return false, err
				}
			}
			f.Removed = true
			times[f] = time.Since(f.start).Seconds()
		}
		return len(times) == len(items), nil
	})
	if err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	values := make([]float64, 0, len(items))
	for _, f := range items {
		values = append(values, times[f])
	}
	b.r.Measurements = append(b.r.Measurements, measurement{Name: name, Seconds: values, P50: quantile(values, .5), P95: quantile(values, .95), Max: quantile(values, 1), Wall: time.Since(wave).Seconds()})
	return nil
}

func (b *runner) remove(ctx context.Context, items []*fixture, name string) error {
	if err := parallel(ctx, b.o.Workers, items, func(ctx context.Context, f *fixture) error {
		if f.Removed {
			return nil
		}
		return b.deleteCR(ctx, f)
	}); err != nil {
		return err
	}
	return b.gone(ctx, items, name)
}

func (b *runner) http(ctx context.Context, f *fixture) error {
	if err := b.integrity(ctx, f); err != nil {
		return err
	}
	d := &appsv1.Deployment{}
	if err := b.c.Get(ctx, client.ObjectKey{Namespace: f.Namespace, Name: controller.AppName}, d); err != nil {
		return err
	}
	if err := sameUID(d, f.DeploymentUIDs[len(f.DeploymentUIDs)-1]); err != nil {
		return err
	}
	if !previewOwned(d, f) {
		return errors.New("foreign HTTP Deployment")
	}
	pods, err := b.deploymentPods(ctx, d)
	if err != nil {
		return err
	}
	var ready []corev1.Pod
	for _, pod := range pods {
		if !pod.DeletionTimestamp.IsZero() || pod.Status.Phase != corev1.PodRunning {
			continue
		}
		for _, condition := range pod.Status.Conditions {
			if condition.Type == corev1.PodReady && condition.Status == corev1.ConditionTrue {
				ready = append(ready, pod)
			}
		}
	}
	if len(ready) == 0 {
		return errors.New("no owned HTTP Pod")
	}
	// exec.Command bypasses MSYS path rewriting; /netprobe must stay literal.
	probeCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(probeCtx, "kubectl", "--kubeconfig", b.o.Kubeconfig, "--context", b.o.Context, "--request-timeout=20s", "-n", f.Namespace, "exec", ready[0].Name, "--", "/netprobe", "http", "http://app."+f.Namespace+".svc.cluster.local:8080", "previewforge-network-witness")
	var stderr strings.Builder
	cmd.Stderr = &stderr
	data, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("HTTP %s: %w: %s", f.Name, err, stderr.String())
	}
	var result struct {
		Status int    `json:"status"`
		Body   string `json:"body"`
	}
	if err = json.Unmarshal(data, &result); err != nil {
		return err
	}
	if result.Status != 200 || !strings.Contains(result.Body, "previewforge-network-witness") {
		return errors.New("unexpected HTTP witness")
	}
	f.HTTP = append(f.HTTP, json.RawMessage(data))
	return nil
}

func (b *runner) deploymentPods(ctx context.Context, d *appsv1.Deployment) ([]corev1.Pod, error) {
	sets := &appsv1.ReplicaSetList{}
	pods := &corev1.PodList{}
	if err := b.c.List(ctx, sets, client.InNamespace(d.Namespace)); err != nil {
		return nil, err
	}
	if err := b.c.List(ctx, pods, client.InNamespace(d.Namespace)); err != nil {
		return nil, err
	}
	var result []corev1.Pod
	for _, p := range pods.Items {
		for _, rs := range sets.Items {
			if owned(&rs, "apps/v1", "Deployment", d.Name, d.UID) && owned(&p, "apps/v1", "ReplicaSet", rs.Name, rs.UID) {
				result = append(result, p)
				break
			}
		}
	}
	return result, nil
}

func (b *runner) scale(ctx context.Context, name string, n int32) error {
	if err := b.source(ctx); err != nil {
		return err
	}
	original, ok := b.controls[name]
	if !ok {
		return errors.New("control Deployment not captured")
	}
	err := retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		d := &appsv1.Deployment{}
		if err := b.c.Get(ctx, client.ObjectKeyFromObject(original), d); err != nil {
			return err
		}
		if err := sameUID(d, original.UID); err != nil {
			return err
		}
		if d.Spec.Replicas != nil && *d.Spec.Replicas == n {
			return nil
		}
		d.Spec.Replicas = new(n)
		return b.c.Update(ctx, d)
	})
	if err != nil {
		return err
	}
	return wait(ctx, 120*time.Second, func(ctx context.Context) (bool, error) {
		d := &appsv1.Deployment{}
		if err := b.c.Get(ctx, client.ObjectKeyFromObject(original), d); err != nil {
			return false, err
		}
		if err := sameUID(d, original.UID); err != nil {
			return false, err
		}
		pods, err := b.deploymentPods(ctx, d)
		if err != nil {
			return false, err
		}
		if n == 0 {
			return len(pods) == 0, nil
		}
		return d.Status.ObservedGeneration == d.Generation && d.Status.ReadyReplicas == n && d.Status.UpdatedReplicas == n && d.Status.AvailableReplicas == n && len(pods) == int(n), nil
	})
}
