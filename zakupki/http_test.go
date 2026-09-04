package zakupki

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestHTTPClientUsesSystemPoolByDefault(t *testing.T) {
	t.Setenv("ZAKUPKI_CA_FILE", "")
	c, err := NewHTTPClientFromEnv(time.Second)
	if err != nil {
		t.Fatal(err)
	}
	transport, ok := c.Transport.(*http.Transport)
	if !ok || transport.TLSClientConfig == nil || transport.TLSClientConfig.RootCAs == nil {
		t.Fatal("expected configured system root pool")
	}
}

func TestHTTPClientAppendsValidCA(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ZAKUPKI_CA_FILE", path)
	if _, err := NewHTTPClientFromEnv(time.Second); err != nil {
		t.Fatal(err)
	}
}

func TestHTTPClientRejectsInvalidOrMissingCA(t *testing.T) {
	for _, value := range []string{"/does/not/exist", "not-a-pem"} {
		t.Setenv("ZAKUPKI_CA_FILE", value)
		_, err := NewHTTPClientFromEnv(time.Second)
		if err == nil {
			t.Fatal("expected CA error")
		}
		if !strings.Contains(err.Error(), "CA file") {
			t.Fatalf("unexpected error: %v", err)
		}
	}
}
