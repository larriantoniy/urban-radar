package zakupki

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/http"
	"os"
	"time"
)

// NewHTTPClientFromEnv creates the shared Zakupki HTTP client. System roots
// remain enabled; ZAKUPKI_CA_FILE only appends an operator-provided CA bundle.
func NewHTTPClientFromEnv(timeout time.Duration) (*http.Client, error) {
	pool, err := x509.SystemCertPool()
	if err != nil {
		return nil, fmt.Errorf("zakupki TLS: load system certificate pool: %w", err)
	}
	if path := os.Getenv("ZAKUPKI_CA_FILE"); path != "" {
		pem, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("zakupki TLS CA file %q: %w", path, err)
		}
		if ok := pool.AppendCertsFromPEM(pem); !ok {
			return nil, fmt.Errorf("zakupki TLS CA file %q: no certificates appended (invalid PEM)", path)
		}
	}
	if timeout <= 0 {
		timeout = 20 * time.Second
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
	return &http.Client{Timeout: timeout, Transport: transport}, nil
}
