// Package monitoring exports bounded, restart-safe inventory metrics from Kubernetes.
package monitoring

import (
	"context"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	platformv1 "github.com/T1mof/previewForge/api/v1alpha1"
)

// This role is generated separately and is not bound to a reader by default.
// +kubebuilder:rbac:urls=/metrics,verbs=get

var phases = [...]string{"Pending", "Deploying", "Ready", "Failed", "Expired", "Terminating", "Unknown"}

// Inventory reads the API on each scrape. It stores no per-environment state and
// exposes no names, UIDs, namespaces, image references or arbitrary status values.
type Inventory struct {
	reader                              client.Reader
	now                                 func() time.Time
	up, count, oldest, overdue, cleanup *prometheus.Desc
}

func NewInventory(reader client.Reader) *Inventory {
	return &Inventory{
		reader: reader, now: time.Now,
		up:      prometheus.NewDesc("previewforge_inventory_up", "Whether the Kubernetes inventory List succeeded on this scrape.", nil, nil),
		count:   prometheus.NewDesc("previewforge_environments", "Current PreviewEnvironment objects by phase; stale status is Deploying.", []string{"phase"}, nil),
		oldest:  prometheus.NewDesc("previewforge_environment_oldest_age_seconds", "Age since creation of the oldest existing environment in each phase.", []string{"phase"}, nil),
		overdue: prometheus.NewDesc("previewforge_environments_ttl_overdue", "Existing environments whose creation-based TTL has elapsed, including terminating objects.", nil, nil),
		cleanup: prometheus.NewDesc("previewforge_cleanup_oldest_age_seconds", "Longest time since deletionTimestamp among terminating environments.", nil, nil),
	}
}

func (m *Inventory) Describe(ch chan<- *prometheus.Desc) {
	for _, desc := range []*prometheus.Desc{m.up, m.count, m.oldest, m.overdue, m.cleanup} {
		ch <- desc
	}
}

func (m *Inventory) Collect(ch chan<- prometheus.Metric) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	list := &platformv1.PreviewEnvironmentList{}
	if err := m.reader.List(ctx, list); err != nil {
		// Missing inventory is not an empty cluster. Omit values instead of lying
		// with zeros; the separate up metric distinguishes the failed API read.
		ch <- prometheus.MustNewConstMetric(m.up, prometheus.GaugeValue, 0)
		return
	}
	now := m.now()
	counts, ages := map[string]float64{}, map[string]float64{}
	var overdue, cleanup float64
	for i := range list.Items {
		p := &list.Items[i]
		phase := phaseOf(p)
		counts[phase]++
		if !p.CreationTimestamp.IsZero() {
			ages[phase] = max(ages[phase], nonnegativeAge(now, p.CreationTimestamp.Time))
			ttl := p.Spec.TTLSeconds
			if ttl == 0 {
				ttl = 3600
			}
			if ttl > 0 && ttl <= 604800 && !now.Before(p.CreationTimestamp.Add(time.Duration(ttl)*time.Second)) {
				overdue++
			}
		}
		if !p.DeletionTimestamp.IsZero() {
			cleanup = max(cleanup, nonnegativeAge(now, p.DeletionTimestamp.Time))
		}
	}
	ch <- prometheus.MustNewConstMetric(m.up, prometheus.GaugeValue, 1)
	for _, phase := range phases {
		ch <- prometheus.MustNewConstMetric(m.count, prometheus.GaugeValue, counts[phase], phase)
		ch <- prometheus.MustNewConstMetric(m.oldest, prometheus.GaugeValue, ages[phase], phase)
	}
	ch <- prometheus.MustNewConstMetric(m.overdue, prometheus.GaugeValue, overdue)
	ch <- prometheus.MustNewConstMetric(m.cleanup, prometheus.GaugeValue, cleanup)
}

func nonnegativeAge(now, then time.Time) float64 { return max(0, now.Sub(then).Seconds()) }

func phaseOf(p *platformv1.PreviewEnvironment) string {
	if !p.DeletionTimestamp.IsZero() {
		return "Terminating"
	}
	if p.Status.Phase == "" {
		return "Pending"
	}
	if p.Status.ObservedGeneration != p.Generation {
		return "Deploying"
	}
	if p.Status.Phase == platformv1.PhaseReady {
		condition := meta.FindStatusCondition(p.Status.Conditions, "Ready")
		if condition == nil || condition.Status != metav1.ConditionTrue || condition.ObservedGeneration != p.Generation {
			return "Deploying"
		}
	}
	for _, phase := range phases[:5] {
		if string(p.Status.Phase) == phase {
			return phase
		}
	}
	return "Unknown"
}
