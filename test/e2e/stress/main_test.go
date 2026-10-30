package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

func TestNearestRank(t *testing.T) {
	values := []float64{7, 1, 9, 2, 3, 6, 5, 4, 8, 10, 16, 15, 14, 13, 12, 11}
	original := append([]float64(nil), values...)
	if quantile(values, .5) != 8 || quantile(values, .95) != 16 || quantile(values, 1) != 16 || quantile(nil, .95) != 0 || quantile([]float64{4}, .5) != 4 {
		t.Fatal("incorrect nearest-rank percentile")
	}
	if !reflect.DeepEqual(values, original) {
		t.Fatal("percentile calculation reordered raw observations")
	}
}

func TestParallelBoundAndError(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	items := make([]*fixture, 16)
	for i := range items {
		items[i] = &fixture{Name: "fixture"}
	}
	var active, peak, count atomic.Int32
	gate := make(chan struct{})
	go func() {
		for peak.Load() < 4 {
			select {
			case <-ctx.Done():
				close(gate)
				return
			case <-time.After(time.Millisecond):
			}
		}
		close(gate)
	}()
	witness := errors.New("write conflict witness")
	err := parallel(ctx, 4, items, func(context.Context, *fixture) error {
		n := active.Add(1)
		defer active.Add(-1)
		for old := peak.Load(); n > old; old = peak.Load() {
			if peak.CompareAndSwap(old, n) {
				break
			}
		}
		<-gate
		count.Add(1)
		return witness
	})
	if !errors.Is(err, witness) || peak.Load() != 4 || count.Load() != 16 {
		t.Fatalf("bound/propagation: peak=%d count=%d err=%v", peak.Load(), count.Load(), err)
	}
	canceled, stop := context.WithCancel(context.Background())
	stop()
	if err = parallel(canceled, 4, items, func(ctx context.Context, _ *fixture) error { return ctx.Err() }); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation lost")
	}
}

func TestRunAndEndpointBoundaries(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	o := options{Cluster: "previewforge-test-stress-unit", Context: "kind-previewforge-test-stress-unit", SourceUID: "source-original", Namespace: "previewforge-system", Count: 16, Workers: 4, Image: "previewforge-netprobe:previewforge-test-stress-unit"}
	o.RunDir = filepath.Join(root, "bin", "runs", o.Cluster)
	o.Kubeconfig = filepath.Join(o.RunDir, "kubeconfig")
	if err = os.MkdirAll(o.RunDir, 0700); err != nil {
		t.Fatal(err)
	}
	token := "0123456789abcdef0123456789abcdef"
	for path, data := range map[string]string{o.Kubeconfig: "test fixture only", filepath.Join(o.RunDir, ".cluster-owner"): token} {
		if err = os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err = validate(o, root, token); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*options){func(o *options) { o.Context = "production" }, func(o *options) { o.Cluster = "production" }, func(o *options) { o.RunDir = root }, func(o *options) { o.Kubeconfig = filepath.Join(root, "user-config") }, func(o *options) { o.SourceUID = "" }, func(o *options) { o.Workers = 9 }, func(o *options) { o.Count = 100 }, func(o *options) { o.Image = "nginx:alpine" }} {
		bad := o
		mutate(&bad)
		if validate(bad, root, token) == nil {
			t.Fatalf("unsafe profile accepted: %+v", bad)
		}
	}
	if validate(o, root, "ffffffffffffffffffffffffffffffff") == nil {
		t.Fatal("wrong ownership claim accepted")
	}
	for _, host := range []string{"https://127.0.0.1:1234", "https://[::1]:1234", "https://localhost:1234"} {
		if err = localEndpoint(&rest.Config{Host: host}); err != nil {
			t.Fatal(err)
		}
	}
	for _, host := range []string{"http://127.0.0.1:1234", "https://cluster.example", "https://192.0.2.1:6443"} {
		if localEndpoint(&rest.Config{Host: host}) == nil {
			t.Fatalf("external or insecure endpoint accepted: %s", host)
		}
	}
	if localEndpoint(&rest.Config{Host: "https://127.0.0.1:1234", TLSClientConfig: rest.TLSClientConfig{Insecure: true}}) == nil {
		t.Fatal("TLS verification bypass accepted")
	}
}

func TestOriginalUIDDeletePreconditions(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	var calls int
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "fixture", UID: "original", ResourceVersion: "42"}}).WithInterceptorFuncs(interceptor.Funcs{
		Delete: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.DeleteOption) error {
			options := &client.DeleteOptions{}
			options.ApplyOptions(opts)
			if options.Preconditions == nil || options.Preconditions.UID == nil || *options.Preconditions.UID != "original" || options.Preconditions.ResourceVersion == nil || *options.Preconditions.ResourceVersion != "42" {
				t.Fatal("delete omitted original UID / current RV")
			}
			calls++
			return c.Delete(ctx, obj, opts...)
		},
	}).Build()
	obj := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "fixture"}}
	if err := deleteOriginal(context.Background(), c, obj, "replacement"); err == nil || calls != 0 {
		t.Fatal("replacement UID was eligible for deletion")
	}
	if err := deleteOriginal(context.Background(), c, obj, "original"); err != nil || calls != 1 {
		t.Fatalf("original delete failed: %v calls=%d", err, calls)
	}
	ref := metav1.OwnerReference{APIVersion: "apps/v1", Kind: "Deployment", Name: "app", UID: "owner", Controller: new(true)}
	obj.OwnerReferences = []metav1.OwnerReference{ref}
	if !owned(obj, "apps/v1", "Deployment", "app", "owner") || owned(obj, "v1", "Deployment", "app", "owner") || owned(obj, "apps/v1", "Deployment", "app", "replacement") {
		t.Fatal("ownership check did not bind type and UID")
	}
}
