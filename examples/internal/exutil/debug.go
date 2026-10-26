package exutil

import (
	"crypto/tls"
	"fmt"
	"net/http"
	"net/http/httputil"
	"os"
	"time"
)

// debugHTTPClient returns a client that dumps every request and response to stderr.
func debugHTTPClient() *http.Client {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	return &http.Client{Transport: dumpTransport{tr}, Timeout: 30 * time.Second}
}

type dumpTransport struct{ rt http.RoundTripper }

func (d dumpTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	shown := req
	if req.Header.Get("Authorization") != "" {
		shown = req.Clone(req.Context())
		shown.Header.Set("Authorization", "Bearer <redacted>")
	}
	b, _ := httputil.DumpRequestOut(shown, true)
	fmt.Fprintf(os.Stderr, "\n>>> %s\n", b)
	res, err := d.rt.RoundTrip(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "<<< transport error: %v\n", err)
		return nil, err
	}
	b, _ = httputil.DumpResponse(res, true)
	fmt.Fprintf(os.Stderr, "<<< %s\n", b)
	return res, nil
}
