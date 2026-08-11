package syncserver

import (
	"crypto/tls"
	"net/http"
	"sync"
	"time"

	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/http3"
)

var (
	transportOnce sync.Once
	dualTransport *dualRoundTripper
)

func initTransports() {
	h3 := &http3.Transport{
		TLSClientConfig: &tls.Config{},
		QUICConfig: &quic.Config{
			Allow0RTT: true,
		},
	}

	h2 := &http.Transport{
		TLSClientConfig:   &tls.Config{},
		ForceAttemptHTTP2: true,
		MaxIdleConns:      10,
		MaxIdleConnsPerHost: 5,
		IdleConnTimeout:   90 * time.Second,
	}

	dualTransport = &dualRoundTripper{h3: h3, h2: h2}
}

// dualRoundTripper tries HTTP/3 (QUIC) first, falls back to HTTP/2 on failure.
// Once a protocol succeeds for a host, it's remembered to avoid retry overhead.
type dualRoundTripper struct {
	h3 http.RoundTripper
	h2 http.RoundTripper

	mu     sync.Mutex
	h3Ok   map[string]bool // hosts where H3 worked
	h2Only map[string]bool // hosts where H3 failed (use H2 directly)
}

func (d *dualRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	host := req.URL.Host

	d.mu.Lock()
	h3Failed := d.h2Only[host]
	h3Succeeded := d.h3Ok[host]
	d.mu.Unlock()

	// If we already know H3 works for this host, use it directly
	if h3Succeeded {
		return d.h3.RoundTrip(req)
	}

	// If H3 already failed for this host, skip straight to H2
	if h3Failed {
		return d.h2.RoundTrip(req)
	}

	// First attempt: try H3, fall back to H2 on any error
	resp, err := d.h3.RoundTrip(req)
	if err != nil {
		d.mu.Lock()
		if d.h2Only == nil {
			d.h2Only = make(map[string]bool)
		}
		d.h2Only[host] = true
		d.mu.Unlock()

		// Fallback to H2
		return d.h2.RoundTrip(req)
	}

	// H3 worked — remember for future requests
	d.mu.Lock()
	if d.h3Ok == nil {
		d.h3Ok = make(map[string]bool)
	}
	d.h3Ok[host] = true
	d.mu.Unlock()

	return resp, nil
}

// NewHTTP3Client returns an *http.Client that tries HTTP/3 (QUIC) first
// and falls back to HTTP/2 or HTTP/1.1 if QUIC is unavailable.
// Once a protocol succeeds for a host, it's cached to skip discovery.
func NewHTTP3Client(timeout time.Duration) *http.Client {
	transportOnce.Do(initTransports)

	return &http.Client{
		Timeout:   timeout,
		Transport: dualTransport,
	}
}
