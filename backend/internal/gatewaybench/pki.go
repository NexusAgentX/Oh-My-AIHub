package gatewaybench

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"
	"math/big"
	"time"
)

// PKI is a throwaway certificate authority and one server certificate it
// issued. The gateway under test trusts Roots; the fake upstreams serve
// Certificate. It is the test CA of the baseline: the outbound policy only ever
// talks HTTPS, so the fake upstreams have to present a certificate that
// verifies for the host name in the channel's Base URL.
type PKI struct {
	Roots       *x509.CertPool
	Certificate tls.Certificate
}

// NewPKI issues a server certificate valid for the given host names.
func NewPKI(hosts ...string) (*PKI, error) {
	if len(hosts) == 0 {
		return nil, fmt.Errorf("gatewaybench: a certificate needs at least one host name")
	}
	now := time.Now()
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	caTemplate := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "gatewaybench test CA"},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(48 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		return nil, err
	}
	caCertificate, err := x509.ParseCertificate(caDER)
	if err != nil {
		return nil, err
	}
	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	leafTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: hosts[0]},
		DNSNames:     hosts,
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     now.Add(48 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leafTemplate, caCertificate, &leafKey.PublicKey, caKey)
	if err != nil {
		return nil, err
	}
	roots := x509.NewCertPool()
	roots.AddCert(caCertificate)
	return &PKI{Roots: roots, Certificate: tls.Certificate{Certificate: [][]byte{leafDER}, PrivateKey: leafKey}}, nil
}

// ServerConfig is the TLS configuration of a fake upstream. It offers HTTP/2
// next to HTTP/1.1, so that a client which asks for it gets it; the gateway
// does not yet.
func (p *PKI) ServerConfig() *tls.Config {
	return &tls.Config{
		MinVersion:   tls.VersionTLS12,
		Certificates: []tls.Certificate{p.Certificate},
		NextProtos:   []string{"h2", "http/1.1"},
	}
}
