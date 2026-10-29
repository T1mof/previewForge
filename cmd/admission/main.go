package main

import (
	"flag"
	"os"

	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	webadmission "sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	platformv1 "github.com/T1mof/previewForge/api/v1alpha1"
	boundary "github.com/T1mof/previewForge/internal/admission"
	"github.com/T1mof/previewForge/internal/controller"
)

func main() {
	var certDir string
	flag.StringVar(&certDir, "cert-dir", "/certs", "Directory containing tls.crt and tls.key")
	flag.Parse()
	ctrl.SetLogger(zap.New())
	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		panic(err)
	}
	if err := platformv1.AddToScheme(scheme); err != nil {
		panic(err)
	}
	reader, err := client.New(ctrl.GetConfigOrDie(), client.Options{Scheme: scheme})
	if err != nil {
		ctrl.Log.Error(err, "create uncached admission reader")
		os.Exit(1)
	}
	server := webhook.NewServer(webhook.Options{Port: 9443, CertDir: certDir})
	server.Register(boundary.Path, &webadmission.Webhook{Handler: &boundary.Ownership{Reader: reader, NamespaceName: controller.NamespaceName}})
	if err := server.Start(ctrl.SetupSignalHandler()); err != nil {
		ctrl.Log.Error(err, "serve admission")
		os.Exit(1)
	}
}
