/*
Command certrotation rotates a server certificate: it compares the
certificate and private key deployed in an ssl-certificate resource with
local PEM files and — only on difference — replaces both. Certificate and
key are two attributes of ONE Airlock resource, updated in a single
PATCH, so the gateway never holds a mismatched pair.

The resource is selected by -name, matched against the numeric resource
ID or the subject CN of the deployed leaf certificate; if nothing
matches, a new ssl-certificate resource is created.

Usage:

	go run ./examples/certrotation -g gateway.example.com -k KEY -name www.example.com \
	    -cert /path/to/fullchain.pem -key /path/to/privkey.pem --activate

By default the change is saved; --activate activates it.
*/
package main

import (
	"crypto/x509"
	"encoding/pem"
	"flag"
	"fmt"
	"log"
	"os"
	"slices"
	"strings"

	"github.com/airlock/gateway-rest-api-lib-go/examples/internal/exutil"
	"github.com/airlock/gateway-rest-api-lib-go/gateway"
)

func main() {
	opts, cf := parseFlags()

	local, err := loadCertPair(opts.certFile, opts.keyFile)
	if err != nil {
		log.Fatal(err)
	}

	s := cf.Setup()
	defer s.Close()

	deployed := findCertificate(s, opts.name)
	switch {
	case deployed == nil:
		createCertificate(s, local)
	case local.deployedOn(*deployed):
		fmt.Println("certificate and key are up to date — nothing to do")
		return
	default:
		rotateCertificate(s, *deployed, local)
	}

	cf.Finish(s)
}

// options holds the command-line configuration of this command.
type options struct {
	name     string
	certFile string
	keyFile  string
}

// parseFlags parses the command line and exits on missing required flags.
func parseFlags() (options, *exutil.CommonFlags) {
	var o options
	cf := exutil.RegisterCommonFlags(flag.CommandLine, "Rotate server certificate")
	flag.StringVar(&o.name, "name", "", "ssl-certificate resource ID or subject CN of the deployed certificate (required)")
	flag.StringVar(&o.certFile, "cert", "", "path to certificate PEM, full chain allowed (required)")
	flag.StringVar(&o.keyFile, "key", "", "path to private key PEM (required)")
	flag.Parse()

	if o.name == "" || o.certFile == "" || o.keyFile == "" {
		log.Fatal("required: -name, -cert, -key")
	}
	return o, cf
}

// certPair is a certificate and its private key as the gateway stores them:
// the leaf certificate, the remaining chain certificates, and the key.
type certPair struct {
	leaf       string
	chain      []string
	privateKey string
}

// loadCertPair reads the PEM files and splits the certificate bundle into
// leaf and chain.
func loadCertPair(certFile, keyFile string) (certPair, error) {
	certPEM, err := os.ReadFile(certFile)
	if err != nil {
		return certPair{}, err
	}
	keyPEM, err := os.ReadFile(keyFile)
	if err != nil {
		return certPair{}, err
	}
	leaf, chain := splitCertBundle(certPEM)
	if leaf == "" {
		return certPair{}, fmt.Errorf("%s: no CERTIFICATE block found", certFile)
	}
	return certPair{leaf: leaf, chain: chain, privateKey: string(keyPEM)}, nil
}

// attributes renders the pair as the attributes of an ssl-certificate resource
func (p certPair) attributes() *gateway.SSLCertificateDto {
	attrs := &gateway.SSLCertificateDto{
		Certificate: gateway.Ptr(p.leaf),
		PrivateKey:  gateway.Ptr(p.privateKey),
	}
	if len(p.chain) > 0 {
		attrs.CertificateChain = gateway.Ptr(p.chain)
	}
	return attrs
}

// deployedOn reports whether res already holds exactly this pair, in which
// case there is nothing to rotate.
func (p certPair) deployedOn(res gateway.ResourceObjectSSLCertificateDto) bool {
	cur := deref(res.Attributes)
	return deref(cur.Certificate) == p.leaf &&
		slices.Equal(deref(cur.CertificateChain), p.chain) &&
		deref(cur.PrivateKey) == p.privateKey
}

// findCertificate returns the ssl-certificate resource selected by name —
// either its numeric resource ID or the subject CN of the deployed leaf
// certificate — or nil if no resource matches. An ambiguous name is fatal.
func findCertificate(s *exutil.Session, name string) *gateway.ResourceObjectSSLCertificateDto {
	certs, err := gateway.ExpectOK(s.GW.GetSSLCertificatesWithResponse(s.Ctx))
	if err != nil {
		s.Fatal("get ssl-certificates: %v", err)
	}
	var matches []gateway.ResourceObjectSSLCertificateDto
	for _, c := range deref(certs.JSON200.Data) {
		if deref(c.ID) == name ||
			strings.EqualFold(leafCN(deref(deref(c.Attributes).Certificate)), name) {
			matches = append(matches, c)
		}
	}
	if len(matches) > 1 {
		s.Fatal("name %q matches %d ssl-certificates; use the resource ID", name, len(matches))
	}
	if len(matches) == 0 {
		return nil
	}
	return &matches[0]
}

// rotateCertificate replaces certificate and key in one PATCH, so the
// gateway never holds a mismatched pair.
func rotateCertificate(s *exutil.Session, res gateway.ResourceObjectSSLCertificateDto, p certPair) {
	id := exutil.ResourceID(res.ID)
	if _, err := gateway.ExpectOK(s.GW.UpdateSSLCertificateWithResponse(s.Ctx, id, gateway.UpdateSSLCertificateJSONRequestBody{
		Data: gateway.ResourceObjectSSLCertificateDto{
			Type:       "ssl-certificate",
			ID:         res.ID,
			Attributes: p.attributes(),
		},
	})); err != nil {
		s.Fatal("update ssl-certificate: %v", err)
	}
	fmt.Printf("rotated certificate and key on ssl-certificate %d\n", id)
}

// createCertificate deploys the pair as a new ssl-certificate resource.
func createCertificate(s *exutil.Session, p certPair) {
	created, err := gateway.ExpectCreated(s.GW.CreateSSLCertificateWithResponse(s.Ctx, gateway.CreateSSLCertificateJSONRequestBody{
		Data: gateway.ResourceObjectSSLCertificateDto{Type: "ssl-certificate", Attributes: p.attributes()},
	}))
	if err != nil {
		s.Fatal("create ssl-certificate: %v", err)
	}
	fmt.Printf("created ssl-certificate %s\n", deref(created.JSON201.Data.ID))
}

// splitCertBundle splits PEM content into the leaf certificate and any
// following chain certificates (the gateway stores the chain separately).
func splitCertBundle(pemBytes []byte) (leaf string, chain []string) {
	rest := pemBytes
	for {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			return leaf, chain
		}
		if block.Type != "CERTIFICATE" {
			continue
		}
		enc := string(pem.EncodeToMemory(block))
		if leaf == "" {
			leaf = enc
		} else {
			chain = append(chain, enc)
		}
	}
}

// leafCN returns the subject CN of the first CERTIFICATE block in pemStr
func leafCN(pemStr string) string {
	rest := []byte(pemStr)
	for {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			return ""
		}
		if block.Type != "CERTIFICATE" {
			continue
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return ""
		}
		return cert.Subject.CommonName
	}
}

// deref returns *p, or the zero value of T if p is nil.
func deref[T any](p *T) T {
	if p == nil {
		var zero T
		return zero
	}
	return *p
}
