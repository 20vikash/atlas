package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadTLSBuildsAPIAndMutualTLSConfigurations(t *testing.T) {
	options := writeTLSFiles(t)

	configurations, err := loadTLS(options)
	if err != nil {
		t.Fatal(err)
	}
	if configurations.api.MinVersion != tls.VersionTLS13 || configurations.api.ClientAuth != tls.NoClientCert {
		t.Fatalf("API TLS config = version %x, client auth %d", configurations.api.MinVersion, configurations.api.ClientAuth)
	}
	if configurations.coordination.ClientAuth != tls.RequireAndVerifyClientCert || configurations.coordination.ClientCAs == nil {
		t.Fatalf("coordination TLS config = client auth %d, CAs %v", configurations.coordination.ClientAuth, configurations.coordination.ClientCAs)
	}
	if configurations.client.RootCAs == nil || len(configurations.client.Certificates) != 1 {
		t.Fatal("client TLS config does not trust the CA and present the node certificate")
	}
}

func TestLoadTLSRejectsACertificateFromAnotherCA(t *testing.T) {
	options := writeTLSFiles(t)
	other := writeTLSFiles(t)
	options.caFile = other.caFile

	if _, err := loadTLS(options); err == nil {
		t.Fatal("certificate from another CA was accepted")
	}
}

func writeTLSFiles(t *testing.T) tlsOptions {
	t.Helper()
	directory := t.TempDir()
	caCertificate, caKey := createTestCertificate(t, nil, nil, true)
	nodeCertificate, nodeKey := createTestCertificate(t, caCertificate, caKey, false)
	caFile := filepath.Join(directory, "ca.crt")
	certificateFile := filepath.Join(directory, "node.crt")
	privateKeyFile := filepath.Join(directory, "node.key")
	writeTestPEM(t, caFile, "CERTIFICATE", caCertificate.Raw)
	writeTestPEM(t, certificateFile, "CERTIFICATE", nodeCertificate.Raw)
	keyBytes, err := x509.MarshalPKCS8PrivateKey(nodeKey)
	if err != nil {
		t.Fatal(err)
	}
	writeTestPEM(t, privateKeyFile, "PRIVATE KEY", keyBytes)
	return tlsOptions{caFile: caFile, certificateFile: certificateFile, privateKeyFile: privateKeyFile}
}

func createTestCertificate(t *testing.T, issuer *x509.Certificate, issuerKey *ecdsa.PrivateKey, isCA bool) (*x509.Certificate, *ecdsa.PrivateKey) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()), Subject: pkix.Name{CommonName: "metal-test"},
		NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour),
		BasicConstraintsValid: true, IsCA: isCA,
	}
	if isCA {
		template.KeyUsage = x509.KeyUsageCertSign
		issuer = template
		issuerKey = key
	} else {
		template.KeyUsage = x509.KeyUsageDigitalSignature
		template.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth}
	}
	der, err := x509.CreateCertificate(rand.Reader, template, issuer, &key.PublicKey, issuerKey)
	if err != nil {
		t.Fatal(err)
	}
	certificate, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return certificate, key
}

func writeTestPEM(t *testing.T, path, blockType string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: blockType, Bytes: data}), 0o600); err != nil {
		t.Fatal(err)
	}
}
