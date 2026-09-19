package main

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
)

type tlsConfigurations struct {
	api          *tls.Config
	coordination *tls.Config
	client       *tls.Config
}

func loadTLS(options tlsOptions) (tlsConfigurations, error) {
	if options.caFile == "" || options.certificateFile == "" || options.privateKeyFile == "" {
		return tlsConfigurations{}, fmt.Errorf("tls.ca_file, tls.certificate_file, and tls.private_key_file are required")
	}
	certificate, err := tls.LoadX509KeyPair(options.certificateFile, options.privateKeyFile)
	if err != nil {
		return tlsConfigurations{}, fmt.Errorf("load Metal TLS certificate: %w", err)
	}
	caPEM, err := os.ReadFile(options.caFile)
	if err != nil {
		return tlsConfigurations{}, fmt.Errorf("read Metal TLS certificate authority: %w", err)
	}
	certificateAuthorities := x509.NewCertPool()
	if !certificateAuthorities.AppendCertsFromPEM(caPEM) {
		return tlsConfigurations{}, fmt.Errorf("Metal TLS certificate authority is invalid")
	}
	leaf, err := x509.ParseCertificate(certificate.Certificate[0])
	if err != nil {
		return tlsConfigurations{}, fmt.Errorf("parse Metal TLS certificate: %w", err)
	}
	for _, usage := range []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth} {
		if _, err := leaf.Verify(x509.VerifyOptions{Roots: certificateAuthorities, KeyUsages: []x509.ExtKeyUsage{usage}}); err != nil {
			return tlsConfigurations{}, fmt.Errorf("verify Metal TLS certificate: %w", err)
		}
	}

	base := &tls.Config{
		MinVersion:   tls.VersionTLS13,
		Certificates: []tls.Certificate{certificate},
	}
	api := base.Clone()
	coordination := base.Clone()
	coordination.ClientAuth = tls.RequireAndVerifyClientCert
	coordination.ClientCAs = certificateAuthorities
	client := base.Clone()
	client.RootCAs = certificateAuthorities

	return tlsConfigurations{api: api, coordination: coordination, client: client}, nil
}
