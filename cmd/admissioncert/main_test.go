package main

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	admissionv1 "k8s.io/api/admissionregistration/v1"
	corev1 "k8s.io/api/core/v1"
)

func TestDemoCertificate(t *testing.T) {
	dir := t.TempDir()
	if err := generate(dir); err != nil {
		t.Fatal(err)
	}
	secretRaw, err := os.ReadFile(filepath.Join(dir, "admission-tls.json"))
	if err != nil {
		t.Fatal(err)
	}
	var secret corev1.Secret
	if err := json.Unmarshal(secretRaw, &secret); err != nil {
		t.Fatal(err)
	}
	pair, err := tls.X509KeyPair(secret.Data["tls.crt"], secret.Data["tls.key"])
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	patchRaw, err := os.ReadFile(filepath.Join(dir, "admission-ca.json"))
	if err != nil {
		t.Fatal(err)
	}
	var patch admissionv1.ValidatingWebhookConfiguration
	if err := json.Unmarshal(patchRaw, &patch); err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	if len(patch.Webhooks) != 1 || !pool.AppendCertsFromPEM(patch.Webhooks[0].ClientConfig.CABundle) {
		t.Fatal("CA patch missing")
	}
	if _, err := leaf.Verify(x509.VerifyOptions{Roots: pool, DNSName: "previewforge-admission.previewforge-system.svc"}); err != nil {
		t.Fatal(err)
	}
	if _, err := leaf.Verify(x509.VerifyOptions{Roots: pool, DNSName: "foreign.example.test"}); err == nil {
		t.Fatal("certificate trusted for a foreign name")
	}
	if _, err := leaf.Verify(x509.VerifyOptions{Roots: pool, DNSName: "previewforge-admission.previewforge-system.svc", CurrentTime: time.Now().Add(49 * time.Hour)}); err == nil {
		t.Fatal("demo certificate should expire")
	}
	if err := generate(dir); err == nil {
		t.Fatal("must not overwrite a run certificate")
	}
	unchanged, err := os.ReadFile(filepath.Join(dir, "admission-tls.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(unchanged) != string(secretRaw) {
		t.Fatal("existing private key overwritten")
	}
}
