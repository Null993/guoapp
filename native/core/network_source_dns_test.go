package core

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestSourceDNSRecoveryKeepsStrictTLSAndOrigin(t *testing.T) {
	for _, host := range []string{"huangguoai.com", "video.huangju.net", "cdn.huangguoai.fit"} {
		t.Run(host, func(t *testing.T) {
			public, private, err := ed25519.GenerateKey(rand.Reader)
			if err != nil {
				t.Fatal(err)
			}
			template := &x509.Certificate{
				SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: host}, DNSNames: []string{host},
				NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
				KeyUsage:    x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
				ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, IsCA: true, BasicConstraintsValid: true,
			}
			der, err := x509.CreateCertificate(rand.Reader, template, template, public, private)
			if err != nil {
				t.Fatal(err)
			}
			certificate, err := x509.ParseCertificate(der)
			if err != nil {
				t.Fatal(err)
			}
			roots := x509.NewCertPool()
			roots.AddCert(certificate)
			good := httptest.NewUnstartedServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				if request.Host != host || request.TLS.ServerName != host || request.Header.Get("Referer") != "https://"+host+"/" {
					t.Error("source recovery changed verified origin or request headers")
				}
				io.WriteString(writer, "synthetic verified media")
			}))
			good.TLS = &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: private}}}
			good.StartTLS()
			defer good.Close()
			wrong := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				t.Error("request reached untrusted origin after certificate failure")
			}))
			defer wrong.Close()
			empty := httptest.NewUnstartedServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				writer.WriteHeader(http.StatusNotFound)
			}))
			empty.TLS = good.TLS.Clone()
			empty.StartTLS()
			defer empty.Close()
			for _, initial := range []*httptest.Server{wrong, empty} {
				base := http.DefaultTransport.(*http.Transport).Clone()
				base.Proxy = nil
				base.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots}
				base.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
					target := initial.Listener.Addr().String()
					if address == "93.184.216.34:443" {
						target = good.Listener.Addr().String()
					}
					return (&net.Dialer{}).DialContext(ctx, network, target)
				}
				resolver := newDNSResolver(base)
				resolver.client.Transport = sourceFixtureTransport(func(request *http.Request) (*http.Response, error) {
					if request.URL.Query().Get("name") != host || request.Header.Get("Cookie") != "" || request.Header.Get("Authorization") != "" {
						t.Error("DoH query lost host or received source credentials")
					}
					return sourceFixtureResponse(request, 200, `{"Status":0,"Answer":[{"type":1,"TTL":60,"data":"93.184.216.34"}]}`), nil
				})
				transport := newCDNTransport(base, resolver)
				defer transport.CloseIdleConnections()
				request, _ := http.NewRequest(http.MethodGet, "https://"+host+"/synthetic.m3u8", nil)
				request.Header.Set("Referer", "https://"+host+"/")
				response, err := transport.RoundTrip(request)
				if err != nil {
					t.Fatal(err)
				}
				defer response.Body.Close()
				body, err := io.ReadAll(response.Body)
				if err != nil || string(body) != "synthetic verified media" || base.TLSClientConfig.InsecureSkipVerify {
					t.Fatal("source DNS fallback did not recover with strict TLS", err)
				}
			}
		})
	}
}
