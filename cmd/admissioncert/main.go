// admissioncert creates short-lived TLS manifests for a disposable demo only.
package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"flag"
	"fmt"
	"k8s.io/apimachinery/pkg/util/validation"
	"math/big"
	"os"
	"path/filepath"
	"time"
)

func writeExclusive(path string, value any) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	if err := json.NewEncoder(f).Encode(value); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

func generate(directory, namespace string) error {
	if directory == "" {
		return fmt.Errorf("--directory is required")
	}
	if len(validation.IsDNS1123Label(namespace)) != 0 {
		return fmt.Errorf("--namespace must be a valid Kubernetes Namespace name")
	}
	// No private key is printed, committed, or included in the e2e artifacts.
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return err
	}
	now := time.Now()
	ca := &x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: "PreviewForge disposable demo CA"}, NotBefore: now.Add(-5 * time.Minute), NotAfter: now.Add(48 * time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature}
	caDER, err := x509.CreateCertificate(rand.Reader, ca, ca, &key.PublicKey, key)
	if err != nil {
		return err
	}
	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	serial, err = rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return err
	}
	host := "previewforge-admission." + namespace + ".svc"
	leaf := &x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: host}, DNSNames: []string{host, host + ".cluster.local"}, NotBefore: ca.NotBefore, NotAfter: ca.NotAfter, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	leafDER, err := x509.CreateCertificate(rand.Reader, leaf, ca, &leafKey.PublicKey, key)
	if err != nil {
		return err
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(leafKey)
	if err != nil {
		return err
	}
	encode := func(kind string, der []byte) string {
		return base64.StdEncoding.EncodeToString(pem.EncodeToMemory(&pem.Block{Type: kind, Bytes: der}))
	}
	secret := map[string]any{"apiVersion": "v1", "kind": "Secret", "metadata": map[string]string{"name": "previewforge-admission-tls", "namespace": namespace}, "type": "kubernetes.io/tls", "data": map[string]string{"tls.crt": encode("CERTIFICATE", leafDER), "tls.key": encode("PRIVATE KEY", keyDER)}}
	patch := map[string]any{"apiVersion": "admissionregistration.k8s.io/v1", "kind": "ValidatingWebhookConfiguration", "metadata": map[string]string{"name": "previewforge-ownership"}, "webhooks": []any{map[string]any{"name": "ownership.platform.previewforge.dev", "clientConfig": map[string]string{"caBundle": encode("CERTIFICATE", caDER)}}}}
	if err := writeExclusive(filepath.Join(directory, "admission-tls.json"), secret); err != nil {
		return err
	}
	if err := writeExclusive(filepath.Join(directory, "admission-helm-values.json"), map[string]any{"admission": map[string]any{"enabled": true, "tls": map[string]string{"secretName": "previewforge-admission-tls", "caBundle": encode("CERTIFICATE", caDER)}}}); err != nil {
		return err
	}
	return writeExclusive(filepath.Join(directory, "admission-ca.json"), patch)
}

func main() {
	var directory, namespace string
	flag.StringVar(&directory, "directory", "", "Existing run directory for demo TLS manifests")
	flag.StringVar(&namespace, "namespace", "previewforge-system", "Namespace of the demo admission Service")
	flag.Parse()
	if err := generate(directory, namespace); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
