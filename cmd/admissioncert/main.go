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

func generate(directory string) error {
	if directory == "" {
		return fmt.Errorf("--directory is required")
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
	leaf := &x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: "previewforge-admission.previewforge-system.svc"}, DNSNames: []string{"previewforge-admission.previewforge-system.svc", "previewforge-admission.previewforge-system.svc.cluster.local"}, NotBefore: ca.NotBefore, NotAfter: ca.NotAfter, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
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
	secret := map[string]any{"apiVersion": "v1", "kind": "Secret", "metadata": map[string]string{"name": "previewforge-admission-tls", "namespace": "previewforge-system"}, "type": "kubernetes.io/tls", "data": map[string]string{"tls.crt": encode("CERTIFICATE", leafDER), "tls.key": encode("PRIVATE KEY", keyDER)}}
	patch := map[string]any{"apiVersion": "admissionregistration.k8s.io/v1", "kind": "ValidatingWebhookConfiguration", "metadata": map[string]string{"name": "previewforge-ownership"}, "webhooks": []any{map[string]any{"name": "ownership.platform.previewforge.dev", "clientConfig": map[string]string{"caBundle": encode("CERTIFICATE", caDER)}}}}
	if err := writeExclusive(filepath.Join(directory, "admission-tls.json"), secret); err != nil {
		return err
	}
	return writeExclusive(filepath.Join(directory, "admission-ca.json"), patch)
}

func main() {
	var directory string
	flag.StringVar(&directory, "directory", "", "Existing run directory for demo TLS manifests")
	flag.Parse()
	if err := generate(directory); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
