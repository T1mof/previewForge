package monitoring

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	platformv1 "github.com/T1mof/previewForge/api/v1alpha1"
)

func metricClient(t *testing.T, objects ...client.Object) client.Client {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := platformv1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	return fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()
}

func TestInventoryRestartsDeletesAndBoundsLabels(t *testing.T) {
	now := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	ready := &platformv1.PreviewEnvironment{ObjectMeta: metav1.ObjectMeta{Name: "private-pr-name", UID: "one", Generation: 2, CreationTimestamp: metav1.NewTime(now.Add(-2 * time.Hour))}, Spec: platformv1.PreviewEnvironmentSpec{TTLSeconds: 3600}, Status: platformv1.PreviewEnvironmentStatus{Phase: platformv1.PhaseReady, ObservedGeneration: 2, Conditions: []metav1.Condition{{Type: "Ready", Status: metav1.ConditionTrue, ObservedGeneration: 2}}}}
	stale := ready.DeepCopy()
	stale.Name, stale.UID, stale.Generation = "new-head", "two", 3
	terminating := ready.DeepCopy()
	terminating.Name, terminating.UID = "terminating", "three"
	stamp := metav1.NewTime(now.Add(-time.Minute))
	terminating.DeletionTimestamp = &stamp
	terminating.Finalizers = []string{"test/finalizer"}
	unknown := ready.DeepCopy()
	unknown.Name, unknown.UID, unknown.Status.Phase = "unknown", "four", "arbitrary-user-phase"
	c := metricClient(t, ready, stale, terminating, unknown)
	collector := NewInventory(c)
	collector.now = func() time.Time { return now }
	check := func(collector *Inventory, expected string) {
		t.Helper()
		if err := testutil.CollectAndCompare(collector, strings.NewReader(expected), "previewforge_environments"); err != nil {
			t.Fatal(err)
		}
	}
	expected := `# HELP previewforge_environments Current PreviewEnvironment objects by phase; stale status is Deploying.
# TYPE previewforge_environments gauge
previewforge_environments{phase="Pending"} 0
previewforge_environments{phase="Deploying"} 1
previewforge_environments{phase="Ready"} 1
previewforge_environments{phase="Failed"} 0
previewforge_environments{phase="Expired"} 0
previewforge_environments{phase="Terminating"} 1
previewforge_environments{phase="Unknown"} 1
`
	check(collector, expected)
	// A new process reconstructs the exact inventory without replaying events.
	check(NewInventory(c), expected)
	if err := testutil.CollectAndCompare(collector, strings.NewReader(`# HELP previewforge_environments_ttl_overdue Existing environments whose creation-based TTL has elapsed, including terminating objects.
# TYPE previewforge_environments_ttl_overdue gauge
previewforge_environments_ttl_overdue 4
# HELP previewforge_cleanup_oldest_age_seconds Longest time since deletionTimestamp among terminating environments.
# TYPE previewforge_cleanup_oldest_age_seconds gauge
previewforge_cleanup_oldest_age_seconds 60
`), "previewforge_environments_ttl_overdue", "previewforge_cleanup_oldest_age_seconds"); err != nil {
		t.Fatal(err)
	}
	if err := c.Delete(context.Background(), ready); err != nil {
		t.Fatal(err)
	}
	check(collector, strings.Replace(expected, `phase="Ready"} 1`, `phase="Ready"} 0`, 1))
	registry := prometheus.NewPedanticRegistry()
	registry.MustRegister(collector)
	families, err := registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, family := range families {
		for _, metric := range family.Metric {
			for _, label := range metric.Label {
				if label.GetName() != "phase" {
					t.Fatalf("unbounded label: %v", label)
				}
			}
		}
	}
}

type unavailable struct{ client.Reader }

func (unavailable) List(context.Context, client.ObjectList, ...client.ListOption) error {
	return errors.New("API unavailable")
}

func TestUnavailableInventoryDoesNotBecomeAnEmptyCluster(t *testing.T) {
	collector := NewInventory(unavailable{})
	registry := prometheus.NewPedanticRegistry()
	registry.MustRegister(collector)
	families, err := registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	if len(families) != 1 || families[0].GetName() != "previewforge_inventory_up" || families[0].Metric[0].Gauge.GetValue() != 0 {
		t.Fatalf("failed read must omit inventory: %v", families)
	}
}

func TestConcurrentScrapesDuringObjectChanges(t *testing.T) {
	c := metricClient(t)
	registry := prometheus.NewPedanticRegistry()
	registry.MustRegister(NewInventory(c))
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 20 {
				if _, err := registry.Gather(); err != nil {
					t.Error(err)
				}
			}
		})
	}
	for range 20 {
		p := &platformv1.PreviewEnvironment{ObjectMeta: metav1.ObjectMeta{Name: "changing", UID: types.UID("uid"), Generation: 1}}
		if err := c.Create(context.Background(), p); err != nil {
			t.Fatal(err)
		}
		if err := c.Delete(context.Background(), p); err != nil {
			t.Fatal(err)
		}
	}
	wg.Wait()
}
