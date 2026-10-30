// stressbench is a bounded experiment for a newly claimed local kind cluster.
// It is not an operator component, a capacity estimator or an external-cluster tool.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	platform "github.com/T1mof/previewForge/api/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	typedcorev1 "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type options struct {
	Kubeconfig, Context, Cluster, RunDir, SourceUID, Namespace, Image string
	Count, Workers                                                    int
}

type measurement struct {
	Name    string       `json:"name"`
	Seconds []float64    `json:"seconds"`
	P50     float64      `json:"p50_seconds"`
	P95     float64      `json:"p95_seconds"`
	Max     float64      `json:"max_seconds"`
	Wall    float64      `json:"wall_seconds"`
	Proof   []readyProof `json:"proof,omitempty"`
}

type readyProof struct {
	Name                         string      `json:"name"`
	CRUID                        types.UID   `json:"cr_uid"`
	NamespaceUID                 types.UID   `json:"namespace_uid"`
	DeploymentUID                types.UID   `json:"deployment_uid"`
	Generation                   int64       `json:"generation"`
	ObservedGeneration           int64       `json:"observed_generation"`
	DeploymentGeneration         int64       `json:"deployment_generation"`
	DeploymentObservedGeneration int64       `json:"deployment_observed_generation"`
	Replicas                     int32       `json:"replicas"`
	PodUIDs                      []types.UID `json:"ready_pod_uids"`
}

type fixture struct {
	Name           string               `json:"name"`
	UID            types.UID            `json:"uid"`
	Namespace      string               `json:"namespace"`
	NamespaceUID   types.UID            `json:"namespace_uid"`
	DeploymentUIDs []types.UID          `json:"deployment_uids"`
	Children       map[string]types.UID `json:"children"`
	Created        time.Time            `json:"created"`
	TTLDeadline    time.Time            `json:"ttl_deadline,omitempty"`
	Generation     int64                `json:"generation"`
	Replicas       int32                `json:"replicas"`
	Removed        bool                 `json:"removed"`
	HTTP           []json.RawMessage    `json:"http"`
	start          time.Time
	replace        bool
}

type metricsSample struct {
	At     time.Time                  `json:"at"`
	Stage  string                     `json:"stage"`
	Values map[string]json.RawMessage `json:"queries"`
	Error  string                     `json:"error,omitempty"`
}

type report struct {
	Schema        int                     `json:"schema"`
	Success       bool                    `json:"success"`
	Cluster       string                  `json:"cluster"`
	Started       time.Time               `json:"started"`
	Finished      time.Time               `json:"finished"`
	Environments  int                     `json:"environments"`
	Workers       int                     `json:"workers"`
	Profile       string                  `json:"profile"`
	ServerVersion string                  `json:"server_version"`
	Nodes         []corev1.NodeSystemInfo `json:"nodes"`
	Allocatable   []corev1.ResourceList   `json:"node_allocatable"`
	Measurements  []measurement           `json:"measurements"`
	Fixtures      []*fixture              `json:"fixtures"`
	Evidence      map[string]any          `json:"evidence"`
	Metrics       []metricsSample         `json:"metrics"`
	Error         string                  `json:"error,omitempty"`
}

type runner struct {
	o              options
	c              client.Client
	operatorClient client.Client
	core           *typedcorev1.CoreV1Client
	r              report
	controls       map[string]*appsv1.Deployment
	mu             sync.Mutex // sampler only; fixture and measurement writes happen outside sampler
	stage          string
}

func main() {
	o := options{}
	flag.StringVar(&o.Kubeconfig, "kubeconfig", "", "run-specific kubeconfig")
	flag.StringVar(&o.Context, "context", "", "explicit kind context")
	flag.StringVar(&o.Cluster, "cluster", "", "claimed previewforge-test-* cluster")
	flag.StringVar(&o.RunDir, "run-dir", "", "existing bin/runs/<cluster>")
	flag.StringVar(&o.SourceUID, "source-namespace-uid", "", "explicit operator Namespace UID")
	flag.StringVar(&o.Namespace, "operator-namespace", "previewforge-system", "operator Namespace")
	flag.StringVar(&o.Image, "image", "", "already loaded netprobe image")
	flag.IntVar(&o.Count, "environments", 16, "8..32 warm-image environments")
	flag.IntVar(&o.Workers, "workers", 4, "1..8 concurrent fixture writers")
	flag.Parse()
	if err := execute(o); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func validate(o options, root, token string) error {
	if !regexp.MustCompile(`^previewforge-test-[a-z0-9][a-z0-9-]{0,31}$`).MatchString(o.Cluster) || o.Context != "kind-"+o.Cluster {
		return errors.New("fresh kind cluster and matching explicit context required")
	}
	if o.Count < 8 || o.Count > 32 || o.Workers < 1 || o.Workers > 8 || o.SourceUID == "" || o.Namespace == "" || o.Image != "previewforge-netprobe:"+o.Cluster {
		return errors.New("bounded profile, explicit source UID and run-specific probe image required")
	}
	if !regexp.MustCompile(`^[a-f0-9]{32}$`).MatchString(token) {
		return errors.New("invalid run token")
	}
	root, err := filepath.EvalSymlinks(root)
	if err != nil {
		return err
	}
	expected := filepath.Join(root, "bin", "runs", o.Cluster)
	for actual, want := range map[string]string{o.RunDir: expected, o.Kubeconfig: filepath.Join(expected, "kubeconfig")} {
		absolute, err := filepath.Abs(actual)
		if err != nil {
			return err
		}
		resolved, err := filepath.EvalSymlinks(absolute)
		if err != nil {
			return err
		}
		if filepath.Clean(absolute) != want || resolved != want {
			return errors.New("run path or kubeconfig escaped the expected workspace directory")
		}
	}
	claim := filepath.Join(expected, ".cluster-owner")
	info, err := os.Lstat(claim)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errors.New("claim must be a regular file")
	}
	b, err := os.ReadFile(claim)
	if err != nil {
		return err
	}
	if strings.TrimSpace(string(b)) != token {
		return errors.New("run ownership mismatch")
	}
	return nil
}

func localEndpoint(cfg *rest.Config) error {
	u, err := url.Parse(cfg.Host)
	if err != nil {
		return err
	}
	ip := net.ParseIP(u.Hostname())
	if u.Scheme != "https" || cfg.Insecure || (u.Hostname() != "localhost" && (ip == nil || !ip.IsLoopback())) {
		return errors.New("only TLS-verified loopback kind API endpoints are allowed")
	}
	return nil
}

func execute(o options) (result error) {
	root, err := os.Getwd()
	if err != nil {
		return err
	}
	if err = validate(o, root, os.Getenv("PREVIEWFORGE_RUN_TOKEN")); err != nil {
		return err
	}
	// Explicit files and context; no merge with the user's kubeconfig.
	cfg, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(&clientcmd.ClientConfigLoadingRules{ExplicitPath: o.Kubeconfig}, &clientcmd.ConfigOverrides{CurrentContext: o.Context}).ClientConfig()
	if err != nil {
		return err
	}
	if err = localEndpoint(cfg); err != nil {
		return err
	}
	cfg.QPS = 20
	cfg.Burst = 40
	cfg.Timeout = 15 * time.Second
	scheme := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{corev1.AddToScheme, appsv1.AddToScheme, networkingv1.AddToScheme, platform.AddToScheme} {
		if err = add(scheme); err != nil {
			return err
		}
	}
	c, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		return err
	}
	core, err := typedcorev1.NewForConfig(cfg)
	if err != nil {
		return err
	}
	opcfg := rest.CopyConfig(cfg)
	opcfg.Impersonate.UserName = "system:serviceaccount:" + o.Namespace + ":previewforge-controller-manager"
	opc, err := client.New(opcfg, client.Options{Scheme: scheme})
	if err != nil {
		return err
	}
	b := &runner{o: o, c: c, core: core, operatorClient: opc, controls: map[string]*appsv1.Deployment{}, stage: "startup"}
	b.r = report{Schema: 1, Cluster: o.Cluster, Started: time.Now().UTC(), Environments: o.Count, Workers: o.Workers,
		Profile:  "warm preloaded image; restricted; request 10m/16Mi limit 100m/64Mi; replicas 1->2->1->2; 4 TTL fixtures; client QPS20 burst40; poll1s; Prometheus sample5s; nearest-rank percentiles; readiness includes API and polling overhead; sampled metrics are not peaks or capacity limits",
		Evidence: map[string]any{}}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
	defer cancel()
	if err = b.source(ctx); err != nil {
		return err
	}
	// Persist partial evidence even on errors. Restore controls before fixture cleanup.
	defer func() {
		cleanupCtx, done := context.WithTimeout(context.Background(), 3*time.Minute)
		defer done()
		for _, name := range []string{"previewforge-admission", "previewforge-controller-manager"} {
			if original, ok := b.controls[name]; ok {
				result = errors.Join(result, b.scale(cleanupCtx, name, *original.Spec.Replicas))
			}
		}
		if result != nil {
			result = errors.Join(result, b.remove(cleanupCtx, b.r.Fixtures, "failure-cleanup"))
		}
		b.r.Finished = time.Now().UTC()
		b.r.Success = result == nil
		if result != nil {
			b.r.Error = result.Error()
		}
		data, err := json.MarshalIndent(b.r, "", "  ")
		if err == nil {
			err = os.WriteFile(filepath.Join(o.RunDir, "artifacts", "stress-report.json"), append(data, '\n'), 0600)
		}
		result = errors.Join(result, err)
	}()
	versionJSON, err := core.RESTClient().Get().AbsPath("/version").DoRaw(ctx)
	if err != nil {
		return err
	}
	var version struct {
		GitVersion string `json:"gitVersion"`
	}
	if err = json.Unmarshal(versionJSON, &version); err != nil {
		return err
	}
	if version.GitVersion == "" {
		return errors.New("missing Kubernetes server version")
	}
	b.r.ServerVersion = version.GitVersion
	nodes := &corev1.NodeList{}
	if err = c.List(ctx, nodes); err != nil {
		return err
	}
	for _, n := range nodes.Items {
		b.r.Nodes = append(b.r.Nodes, n.Status.NodeInfo)
		b.r.Allocatable = append(b.r.Allocatable, n.Status.Allocatable)
	}
	for _, name := range []string{"previewforge-controller-manager", "previewforge-admission"} {
		d := &appsv1.Deployment{}
		if err = c.Get(ctx, client.ObjectKey{Namespace: o.Namespace, Name: name}, d); err != nil {
			return err
		}
		if d.Spec.Replicas == nil || *d.Spec.Replicas != 1 {
			return fmt.Errorf("expected one ready %s replica", name)
		}
		b.controls[name] = d.DeepCopy()
		b.r.Evidence[name] = d.Spec.Template.Spec.Containers
	}
	sampleCtx, stop := context.WithCancel(ctx)
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		b.sample(sampleCtx)
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-sampleCtx.Done():
				return
			case <-ticker.C:
				b.sample(sampleCtx)
			}
		}
	}()
	defer func() { stop(); <-finished }()
	return b.scenarios(ctx)
}

func quantile(values []float64, p float64) float64 {
	if len(values) == 0 {
		return 0
	}
	copyOf := append([]float64(nil), values...)
	sort.Float64s(copyOf)
	i := int(float64(len(copyOf)) * p)
	if float64(i) < float64(len(copyOf))*p {
		i++
	}
	if i < 1 {
		i = 1
	}
	if i > len(copyOf) {
		i = len(copyOf)
	}
	return copyOf[i-1]
}

func parallel(ctx context.Context, workers int, items []*fixture, fn func(context.Context, *fixture) error) error {
	jobs := make(chan *fixture)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var errs []error
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for item := range jobs {
				if err := fn(ctx, item); err != nil {
					mu.Lock()
					errs = append(errs, fmt.Errorf("%s: %w", item.Name, err))
					mu.Unlock()
				}
			}
		}()
	}
	for _, item := range items {
		select {
		case jobs <- item:
		case <-ctx.Done():
			close(jobs)
			wg.Wait()
			return errors.Join(ctx.Err(), errors.Join(errs...))
		}
	}
	close(jobs)
	wg.Wait()
	return errors.Join(errs...)
}

func (b *runner) phase(s string) { b.mu.Lock(); b.stage = s; b.mu.Unlock(); fmt.Println("stress:", s) }

func (b *runner) source(ctx context.Context) error {
	ns := &corev1.Namespace{}
	if err := b.c.Get(ctx, client.ObjectKey{Name: b.o.Namespace}, ns); err != nil {
		return err
	}
	if string(ns.UID) != b.o.SourceUID || !ns.DeletionTimestamp.IsZero() {
		return errors.New("operator source Namespace UID changed or terminating")
	}
	return nil
}

func (b *runner) sample(ctx context.Context) {
	b.mu.Lock()
	s := metricsSample{At: time.Now().UTC(), Stage: b.stage, Values: map[string]json.RawMessage{}}
	b.mu.Unlock()
	for name, query := range map[string]string{
		"up": `up{job="previewforge"}`, "rss_bytes": `process_resident_memory_bytes{job="previewforge"}`,
		"cpu_seconds": `process_cpu_seconds_total{job="previewforge"}`, "cpu_cores_1m": `rate(process_cpu_seconds_total{job="previewforge"}[1m])`,
		"queue": `workqueue_depth{job="previewforge"}`, "reconcile_errors": `controller_runtime_reconcile_errors_total{job="previewforge"}`,
	} {
		data, err := b.core.RESTClient().Get().AbsPath("/api/v1/namespaces/previewforge-monitoring/services/http:prometheus:9090/proxy/api/v1/query").Param("query", query).DoRaw(ctx)
		if err != nil {
			s.Error = err.Error()
			break
		}
		if !json.Valid(data) {
			s.Error = "invalid Prometheus JSON"
			break
		}
		s.Values[name] = data
	}
	b.mu.Lock()
	b.r.Metrics = append(b.r.Metrics, s)
	b.mu.Unlock()
}

// Hash is an inventory selector only. Authority always comes from original UIDs.
func runLabel() string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(os.Getenv("PREVIEWFORGE_RUN_TOKEN"))))[:12]
}
