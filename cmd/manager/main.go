package main

import (
	"flag"
	"os"

	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
	"sigs.k8s.io/controller-runtime/pkg/metrics/filters"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	platformv1 "github.com/T1mof/previewForge/api/v1alpha1"
	"github.com/T1mof/previewForge/internal/controller"
	"github.com/T1mof/previewForge/internal/monitoring"
)

func main() {
	var metricsAddr, metricsCertDir, probeAddr, leaderNamespace string
	var leaderElection bool
	var dnsNamespace, dnsSelector, gatewayNamespace, gatewaySelector string
	flag.StringVar(&metricsAddr, "metrics-bind-address", "0", "Metrics address; 0 disables the server")
	flag.StringVar(&metricsCertDir, "metrics-cert-path", "", "Directory with tls.crt/tls.key; empty uses an ephemeral self-signed metrics certificate")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "Health probe address")
	flag.BoolVar(&leaderElection, "leader-elect", false, "Enable leader election")
	flag.StringVar(&leaderNamespace, "leader-election-namespace", "previewforge-system", "Namespace for leader election Lease")
	flag.StringVar(&dnsNamespace, "dns-namespace", "kube-system", "Namespace containing Pod-based cluster DNS")
	flag.StringVar(&dnsSelector, "dns-pod-selector", "k8s-app=kube-dns", "Non-empty selector for cluster DNS Pods")
	flag.StringVar(&gatewayNamespace, "ingress-controller-namespace", "", "Trusted Ingress controller namespace; required for preview Ingress")
	flag.StringVar(&gatewaySelector, "ingress-controller-pod-selector", "", "Non-empty trusted Ingress controller Pod selector")
	logOptions := zap.Options{}
	logOptions.BindFlags(flag.CommandLine)
	flag.Parse()
	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&logOptions)))
	log := ctrl.Log.WithName("setup")
	network, err := controller.NewNetworkConfig(dnsNamespace, dnsSelector, gatewayNamespace, gatewaySelector)
	if err != nil {
		log.Error(err, "invalid network configuration")
		os.Exit(1)
	}
	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		log.Error(err, "register Kubernetes scheme")
		os.Exit(1)
	}
	if err := platformv1.AddToScheme(scheme); err != nil {
		log.Error(err, "register PreviewForge scheme")
		os.Exit(1)
	}
	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:                  scheme,
		Metrics:                 metricsserver.Options{BindAddress: metricsAddr, SecureServing: true, CertDir: metricsCertDir, FilterProvider: filters.WithAuthenticationAndAuthorization},
		HealthProbeBindAddress:  probeAddr,
		LeaderElection:          leaderElection,
		LeaderElectionID:        "controller.previewforge.dev",
		LeaderElectionNamespace: leaderNamespace,
	})
	if err != nil {
		log.Error(err, "create manager")
		os.Exit(1)
	}
	r := &controller.PreviewEnvironmentReconciler{Client: mgr.GetClient(), APIReader: mgr.GetAPIReader(), Scheme: scheme, Network: network}
	if err := metrics.Registry.Register(monitoring.NewInventory(mgr.GetAPIReader())); err != nil {
		log.Error(err, "register inventory metrics")
		os.Exit(1)
	}
	if err := r.SetupWithManager(mgr); err != nil {
		log.Error(err, "set up controller")
		os.Exit(1)
	}
	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		log.Error(err, "add health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		log.Error(err, "add readiness check")
		os.Exit(1)
	}
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		log.Error(err, "run manager")
		os.Exit(1)
	}
}
