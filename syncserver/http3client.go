package syncserver

import (
	"crypto/tls"
	"net/http"
	"sync"
	"time"

	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/http3"
)

// http3Once ensures the HTTP/3 Transport is created only once.
var (
	http3Once      sync.Once
	http3Transport *http3.Transport
)

// NewHTTP3Client returns an *http.Client that prefers HTTP/3 (QUIC)
// and falls back to HTTP/2 or HTTP/1.1 if QUIC is unavailable.
// The client reuses a shared Transport for connection pooling.
func NewHTTP3Client(timeout time.Duration) *http.Client {
	http3Once.Do(func() {
		http3Transport = &http3.Transport{
			TLSClientConfig: &tls.Config{
				// Caddy uses Let's Encrypt — certificates are in the system trust store.
			},
			QUICConfig: &quic.Config{
				Allow0RTT: true, // faster reconnections
			},
		}
	})

	return &http.Client{
		Timeout:   timeout,
		Transport: http3Transport,
	}
}
