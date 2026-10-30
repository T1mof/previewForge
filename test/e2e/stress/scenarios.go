package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	platform "github.com/T1mof/previewForge/api/v1alpha1"
	"github.com/T1mof/previewForge/internal/controller"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func (b *runner) scenarios(ctx context.Context) error {
	main := make([]*fixture, b.o.Count)
	for i := range main {
		main[i] = &fixture{Name: fmt.Sprintf("stress-%02d", i)}
		b.r.Fixtures = append(b.r.Fixtures, main[i])
	}
	b.phase("create")
	if err := parallel(ctx, b.o.Workers, main, b.create); err != nil {
		return err
	}
	if err := b.ready(ctx, main, "create-ready"); err != nil {
		return err
	}
	if err := parallel(ctx, b.o.Workers, main, b.http); err != nil {
		return err
	}
	if err := b.setReplicas(ctx, main, 2); err != nil {
		return err
	}
	if err := b.ready(ctx, main, "scale-ready"); err != nil {
		return err
	}
	b.phase("drift")
	if err := parallel(ctx, b.o.Workers, main, func(ctx context.Context, f *fixture) error {
		f.start = time.Now()
		return retry.RetryOnConflict(retry.DefaultBackoff, func() error {
			d := &appsv1.Deployment{}
			if err := b.c.Get(ctx, client.ObjectKey{Namespace: f.Namespace, Name: controller.AppName}, d); err != nil {
				return err
			}
			if !previewOwned(d, f) {
				return errors.New("refusing foreign drift target")
			}
			if err := sameUID(d, f.DeploymentUIDs[len(f.DeploymentUIDs)-1]); err != nil {
				return err
			}
			d.Spec.Replicas = new(int32(3))
			return b.c.Update(ctx, d)
		})
	}); err != nil {
		return err
	}
	if err := b.ready(ctx, main, "drift-repaired"); err != nil {
		return err
	}
	if err := b.admissionFault(ctx, main); err != nil {
		return err
	}
	if err := b.downtime(ctx, main); err != nil {
		return err
	}
	if err := b.remove(ctx, main[:len(main)-4], "cleanup"); err != nil {
		return err
	}
	for _, f := range b.r.Fixtures {
		if !f.Removed {
			return fmt.Errorf("fixture remains: %s", f.Name)
		}
	}
	b.r.Evidence["all_fixtures_removed"] = true
	for _, m := range b.r.Measurements {
		for _, seconds := range m.Seconds {
			if seconds <= 0 || math.IsNaN(seconds) || math.IsInf(seconds, 0) {
				return fmt.Errorf("invalid latency observation in %s", m.Name)
			}
		}
	}
	// Require real metrics before and after the experiment; an empty query is not zero.
	if _, err := b.counter(ctx, "process_resident_memory_bytes"); err != nil {
		return err
	}
	b.phase("complete")
	b.sample(ctx)
	return nil
}

func (b *runner) operatorPods(ctx context.Context) (map[types.UID]int32, error) {
	d := b.controls["previewforge-controller-manager"]
	pods, err := b.deploymentPods(ctx, d)
	if err != nil {
		return nil, err
	}
	result := map[types.UID]int32{}
	for _, p := range pods {
		var restarts int32
		for _, s := range p.Status.ContainerStatuses {
			restarts += s.RestartCount
		}
		result[p.UID] = restarts
	}
	if len(result) != 1 {
		return nil, errors.New("expected one actual manager Pod")
	}
	return result, nil
}

func (b *runner) counter(ctx context.Context, metric string) (float64, error) {
	data, err := b.core.RESTClient().Get().AbsPath("/api/v1/namespaces/previewforge-monitoring/services/http:prometheus:9090/proxy/api/v1/query").Param("query", `sum(`+metric+`{job="previewforge"})`).DoRaw(ctx)
	if err != nil {
		return 0, err
	}
	var result struct {
		Status string `json:"status"`
		Data   struct {
			Result []struct {
				Value []json.RawMessage `json:"value"`
			} `json:"result"`
		} `json:"data"`
	}
	if err = json.Unmarshal(data, &result); err != nil {
		return 0, err
	}
	if result.Status != "success" || len(result.Data.Result) != 1 || len(result.Data.Result[0].Value) != 2 {
		return 0, fmt.Errorf("missing metric %s", metric)
	}
	var text string
	if err = json.Unmarshal(result.Data.Result[0].Value[1], &text); err != nil {
		return 0, err
	}
	n, err := strconv.ParseFloat(text, 64)
	if err != nil || math.IsNaN(n) || math.IsInf(n, 0) {
		return 0, fmt.Errorf("invalid metric %s", metric)
	}
	return n, nil
}

func (b *runner) serviceDryRun(ctx context.Context, f *fixture) error {
	s := &corev1.Service{}
	if err := b.c.Get(ctx, client.ObjectKey{Namespace: f.Namespace, Name: controller.AppName}, s); err != nil {
		return err
	}
	if !previewOwned(s, f) {
		return errors.New("foreign service")
	}
	if err := sameUID(s, f.Children["service"]); err != nil {
		return err
	}
	return b.operatorClient.Update(ctx, s, &client.UpdateOptions{DryRun: []string{metav1.DryRunAll}})
}

func webhookUnavailable(err error) bool {
	return apierrors.IsInternalError(err) && strings.Contains(err.Error(), `failed calling webhook "ownership.platform.previewforge.dev"`)
}

func (b *runner) admissionFault(ctx context.Context, items []*fixture) error {
	b.phase("admission-outage")
	before, err := b.operatorPods(ctx)
	if err != nil {
		return err
	}
	errorsBefore, err := b.counter(ctx, "controller_runtime_reconcile_errors_total")
	if err != nil {
		return err
	}
	if err = b.serviceDryRun(ctx, items[0]); err != nil {
		return fmt.Errorf("healthy admission dry run: %w", err)
	}
	if err = b.scale(ctx, "previewforge-admission", 0); err != nil {
		return err
	}
	err = b.serviceDryRun(ctx, items[0])
	if !webhookUnavailable(err) {
		return fmt.Errorf("expected exact fail-closed transport error, got %v", err)
	}
	b.r.Evidence["admission_fail_closed"] = err.Error()
	if err = b.setReplicas(ctx, items, 1); err != nil {
		return err
	}
	if err = parallel(ctx, b.o.Workers, items[:4], b.deleteDeployment); err != nil {
		return err
	}
	// Keep the admission server unavailable long enough for failed write retries.
	if err = wait(ctx, 8*time.Second, func(ctx context.Context) (bool, error) {
		if err := b.pending(ctx, items, 2); err != nil {
			return false, err
		}
		return false, nil
	}); err != nil && !errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err = b.pending(ctx, items, 2); err != nil {
		return err
	}
	b.r.Evidence["admission_held_pending_generations"] = len(items)
	for _, f := range items {
		f.start = time.Now()
	}
	if err = b.scale(ctx, "previewforge-admission", 1); err != nil {
		return err
	}
	// A ready serving Pod can precede endpoint publication; retry only transport failures.
	if err = wait(ctx, 60*time.Second, func(ctx context.Context) (bool, error) {
		err := b.serviceDryRun(ctx, items[0])
		if err == nil {
			return true, nil
		}
		if webhookUnavailable(err) {
			return false, nil
		}
		return false, err
	}); err != nil {
		return err
	}
	if err = b.ready(ctx, items, "admission-recovery"); err != nil {
		return err
	}
	after, err := b.operatorPods(ctx)
	if err != nil {
		return err
	}
	if len(after) != len(before) {
		return errors.New("manager count changed during admission outage")
	}
	for uid, restarts := range before {
		if now, ok := after[uid]; !ok || now != restarts {
			return errors.New("manager restarted during admission recovery")
		}
	}
	b.r.Evidence["admission_manager_pods_before"] = before
	b.r.Evidence["admission_manager_pods_after"] = after
	var errorsAfter float64
	if err = wait(ctx, 30*time.Second, func(ctx context.Context) (bool, error) {
		var err error
		errorsAfter, err = b.counter(ctx, "controller_runtime_reconcile_errors_total")
		return errorsAfter > errorsBefore, err
	}); err != nil {
		return fmt.Errorf("failed-write metric did not increase: %w", err)
	}
	b.r.Evidence["admission_reconcile_errors_before"] = errorsBefore
	b.r.Evidence["admission_reconcile_errors_after"] = errorsAfter
	return parallel(ctx, b.o.Workers, items, b.http)
}

func (b *runner) pending(ctx context.Context, items []*fixture, previous int32) error {
	for _, f := range items {
		p, err := b.fresh(ctx, f)
		if err != nil {
			return err
		}
		if p.Status.ObservedGeneration == p.Generation && p.Status.Phase == platform.PhaseReady {
			return fmt.Errorf("new generation incorrectly Ready during outage: %s", f.Name)
		}
		d := &appsv1.Deployment{}
		err = b.c.Get(ctx, client.ObjectKey{Namespace: f.Namespace, Name: controller.AppName}, d)
		if f.replace {
			if !apierrors.IsNotFound(err) {
				return fmt.Errorf("deleted Deployment recreated during outage: %s (%v)", f.Name, err)
			}
			continue
		}
		if err != nil {
			return err
		}
		if !previewOwned(d, f) {
			return errors.New("foreign pending Deployment")
		}
		if err = sameUID(d, f.DeploymentUIDs[len(f.DeploymentUIDs)-1]); err != nil {
			return err
		}
		if d.Spec.Replicas == nil || *d.Spec.Replicas != previous {
			return fmt.Errorf("pending Deployment changed while writes unavailable: %s", f.Name)
		}
	}
	return nil
}

func (b *runner) downtime(ctx context.Context, items []*fixture) (result error) {
	ttl := make([]*fixture, 4)
	for i := range ttl {
		ttl[i] = &fixture{Name: fmt.Sprintf("stress-ttl-%02d", i)}
		b.r.Fixtures = append(b.r.Fixtures, ttl[i])
	}
	if err := parallel(ctx, b.o.Workers, ttl, b.create); err != nil {
		return err
	}
	if err := b.ready(ctx, ttl, "ttl-warmup"); err != nil {
		return err
	}
	b.phase("operator-downtime")
	before, err := b.operatorPods(ctx)
	if err != nil {
		return err
	}
	// This Namespace is foreign to the operator; test cleanup uses creation UID only.
	canary := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "forge-stress-canary", Labels: map[string]string{controller.ManagedByLabel: "previewforge", controller.OwnerUIDLabel: string(items[len(items)-1].UID)}}}
	if err = b.c.Create(ctx, canary); err != nil {
		return err
	}
	cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "canary", Namespace: canary.Name}, Data: map[string]string{"sentinel": "untouched"}}
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		result = errors.Join(result, deleteOriginal(cleanupCtx, b.c, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: canary.Name}}, canary.UID))
	}()
	if err = b.c.Create(ctx, cm); err != nil {
		return err
	}
	if err = wait(ctx, 30*time.Second, func(ctx context.Context) (bool, error) {
		actual := &corev1.Namespace{}
		if err := b.c.Get(ctx, client.ObjectKeyFromObject(canary), actual); err != nil {
			return false, err
		}
		if err := sameUID(actual, canary.UID); err != nil {
			return false, err
		}
		if actual.Status.Phase != corev1.NamespaceActive {
			return false, nil
		}
		canary = actual
		return true, nil
	}); err != nil {
		return err
	}
	if err = b.scale(ctx, "previewforge-controller-manager", 0); err != nil {
		return err
	}
	stopped := time.Now().UTC()
	survivors, manual := items[:len(items)-4], items[len(items)-4:]
	if err = b.setReplicas(ctx, survivors, 2); err != nil {
		return err
	}
	if err = parallel(ctx, b.o.Workers, survivors[len(survivors)-4:], b.deleteDeployment); err != nil {
		return err
	}
	if err = parallel(ctx, b.o.Workers, manual, b.deleteCR); err != nil {
		return err
	}
	latest := time.Time{}
	for _, f := range ttl {
		if err = b.update(ctx, f, func(p *platform.PreviewEnvironment) {
			p.Spec.TTLSeconds = int64(math.Ceil(time.Since(p.CreationTimestamp.Time).Seconds())) + 10
			f.TTLDeadline = p.CreationTimestamp.Add(time.Duration(p.Spec.TTLSeconds) * time.Second)
		}); err != nil {
			return err
		}
		if f.TTLDeadline.After(latest) {
			latest = f.TTLDeadline
		}
	}
	// Poison mutable status on a deleting CR; neither it nor forged labels authorize deletion.
	spoof := manual[len(manual)-1]
	if err = retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		p, err := b.fresh(ctx, spoof)
		if err != nil {
			return err
		}
		p.Status.Namespace = canary.Name
		return b.c.Status().Update(ctx, p)
	}); err != nil {
		return err
	}
	if err = b.http(ctx, survivors[0]); err != nil {
		return fmt.Errorf("data plane during manager downtime: %w", err)
	}
	if delay := time.Until(latest.Add(2 * time.Second)); delay > 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
		}
	}
	if err = b.pending(ctx, survivors, 1); err != nil {
		return err
	}
	for _, f := range append(append([]*fixture{}, manual...), ttl...) {
		p, err := b.fresh(ctx, f)
		if err != nil {
			return err
		}
		if !contains(p.Finalizers, controller.Finalizer) {
			return errors.New("cleanup finalizer absent during manager downtime")
		}
		if containsFixture(manual, f) && p.DeletionTimestamp.IsZero() {
			return errors.New("manual deletion was not pending")
		}
		n := &corev1.Namespace{}
		if err = b.c.Get(ctx, client.ObjectKey{Name: f.Namespace}, n); err != nil {
			return err
		}
		if err = sameUID(n, f.NamespaceUID); err != nil {
			return err
		}
		if !previewOwned(n, f) {
			return errors.New("foreign downtime namespace")
		}
	}
	b.r.Evidence["downtime_observations"] = map[string]any{"stopped": stopped, "ttl_deadlines_passed_at": time.Now().UTC(), "pending_updates": len(survivors), "manual_deletes_held": len(manual), "ttl_namespaces_held": len(ttl), "http_while_stopped": true, "spoofed_cr": spoof.Name, "spoofed_namespace": canary.Name}
	for _, f := range append(append([]*fixture{}, survivors...), append(manual, ttl...)...) {
		f.start = time.Now()
	}
	if err = b.scale(ctx, "previewforge-controller-manager", 1); err != nil {
		return err
	}
	if err = b.ready(ctx, survivors, "operator-recovery"); err != nil {
		return err
	}
	if err = b.gone(ctx, append(append([]*fixture{}, manual...), ttl...), "backlog-cleanup"); err != nil {
		return err
	}
	after, err := b.operatorPods(ctx)
	if err != nil {
		return err
	}
	for uid := range after {
		if _, ok := before[uid]; ok {
			return errors.New("manager Pod UID did not change")
		}
	}
	b.r.Evidence["operator_pods_before"] = before
	b.r.Evidence["operator_pods_after"] = after
	actualNS := &corev1.Namespace{}
	actualCM := &corev1.ConfigMap{}
	if err = b.c.Get(ctx, client.ObjectKeyFromObject(canary), actualNS); err != nil {
		return err
	}
	if err = b.c.Get(ctx, client.ObjectKeyFromObject(cm), actualCM); err != nil {
		return err
	}
	if actualNS.UID != canary.UID || actualNS.ResourceVersion != canary.ResourceVersion || actualCM.UID != cm.UID || actualCM.ResourceVersion != cm.ResourceVersion || actualCM.Data["sentinel"] != "untouched" {
		return errors.New("foreign canary mutated")
	}
	b.r.Evidence["foreign_canary"] = map[string]any{"namespace_uid": canary.UID, "namespace_rv_before": canary.ResourceVersion, "namespace_rv_after": actualNS.ResourceVersion, "configmap_uid": cm.UID, "configmap_rv_before": cm.ResourceVersion, "configmap_rv_after": actualCM.ResourceVersion, "preserved": true}
	return parallel(ctx, b.o.Workers, survivors, b.http)
}

func contains(items []string, s string) bool {
	for _, item := range items {
		if item == s {
			return true
		}
	}
	return false
}
func containsFixture(items []*fixture, f *fixture) bool {
	for _, item := range items {
		if item == f {
			return true
		}
	}
	return false
}
