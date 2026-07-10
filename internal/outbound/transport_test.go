//nolint:exhaustruct,testpackage // Transport tests use concise standard-library fixtures and internal helpers.
package outbound

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const (
	publicTestHost = "public.example"
	publicTestIP   = "93.184.216.34"
)

var errUnexpectedHostHeader = errors.New("unexpected Host header")

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestClientPinsValidatedAddressAndDisablesProxy(t *testing.T) {
	t.Parallel()

	dialed := make(chan string, 1)
	served := make(chan error, 1)

	var proxyCalled atomic.Bool

	base := &http.Transport{
		Proxy: func(*http.Request) (*url.URL, error) {
			proxyCalled.Store(true)

			return url.Parse("http://127.0.0.1:3128")
		},
	}
	client := NewClient(ClientOptions{
		BaseTransport: base,
		Resolver:      staticResolver(publicTestIP),
		DialContext: func(_ context.Context, _, address string) (net.Conn, error) {
			dialed <- address

			clientConn, serverConn := net.Pipe()
			go servePipeHTTP(serverConn, publicTestHost, served)

			return clientConn, nil
		},
		Timeout: time.Second,
	})

	resp, err := client.Do(newGetRequest(t, "http://"+publicTestHost+"/feed.xml"))
	if err != nil {
		t.Fatalf("get pinned HTTP target: %v", err)
	}
	defer closeResponse(t, resp)

	body, err := io.ReadAll(resp.Body)
	if err != nil || string(body) != "ok" {
		t.Fatalf("read pinned response: body=%q err=%v", body, err)
	}

	if address := <-dialed; address != publicTestIP+":80" {
		t.Fatalf("dialed %q, want approved address", address)
	}

	serveErr := <-served
	if serveErr != nil {
		t.Fatalf("serve pinned request: %v", serveErr)
	}

	if proxyCalled.Load() {
		t.Fatal("environment-style proxy hook was called")
	}
}

func TestClientPreservesTLSHostnameVerificationWhenDialingPinnedIP(t *testing.T) {
	t.Parallel()

	hostHeader := make(chan string, 1)
	writeResult := make(chan error, 1)
	server, base := newTLSTestServer(t, publicTestHost, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hostHeader <- r.Host

		_, writeErr := io.WriteString(w, "secure")
		writeResult <- writeErr
	}))
	dialed := make(chan string, 1)
	client := NewClient(ClientOptions{
		BaseTransport: base,
		Resolver:      staticResolver(publicTestIP),
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			dialed <- address

			var dialer net.Dialer

			return dialer.DialContext(ctx, network, server.Listener.Addr().String())
		},
		Timeout: 2 * time.Second,
	})

	resp, err := client.Do(newGetRequest(t, "https://"+publicTestHost+"/image.png"))
	if err != nil {
		t.Fatalf("get pinned HTTPS target: %v", err)
	}
	defer closeResponse(t, resp)

	body, err := io.ReadAll(resp.Body)
	if err != nil || string(body) != "secure" {
		t.Fatalf("read pinned HTTPS response: body=%q err=%v", body, err)
	}

	if address := <-dialed; address != publicTestIP+":443" {
		t.Fatalf("dialed %q, want approved HTTPS address", address)
	}

	if host := <-hostHeader; host != publicTestHost {
		t.Fatalf("Host header = %q, want %q", host, publicTestHost)
	}

	writeErr := <-writeResult
	if writeErr != nil {
		t.Fatalf("write HTTPS response: %v", writeErr)
	}
}

func TestClientRevalidatesRedirectsAgainstDNSRebinding(t *testing.T) {
	t.Parallel()

	var lookups atomic.Int32

	resolver := LookupIPAddrFunc(func(_ context.Context, _ string) ([]net.IPAddr, error) {
		if lookups.Add(1) == 1 {
			return ipAddrs(publicTestIP), nil
		}

		return ipAddrs("127.0.0.1"), nil
	})

	var roundTrips atomic.Int32

	client := NewClient(ClientOptions{
		BaseTransport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			roundTrips.Add(1)

			return redirectResponse(req, "/next"), nil
		}),
		Resolver: resolver,
	})

	resp, err := client.Do(newGetRequest(t, "http://"+publicTestHost+"/start"))
	closeResponse(t, resp)

	if !errors.Is(err, ErrDestinationNotPublic) {
		t.Fatalf("expected rebinding to be blocked, got %v", err)
	}

	if count := roundTrips.Load(); count != 1 {
		t.Fatalf("round trips = %d, want 1", count)
	}
}

func TestClientRejectsRedirectToPrivateHost(t *testing.T) {
	t.Parallel()

	resolver := LookupIPAddrFunc(func(_ context.Context, host string) ([]net.IPAddr, error) {
		if host == publicTestHost {
			return ipAddrs(publicTestIP), nil
		}

		return ipAddrs("10.0.0.2"), nil
	})

	var roundTrips atomic.Int32

	client := NewClient(ClientOptions{
		BaseTransport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			roundTrips.Add(1)

			return redirectResponse(req, "http://private.example/admin"), nil
		}),
		Resolver: resolver,
	})

	resp, err := client.Do(newGetRequest(t, "http://"+publicTestHost+"/start"))
	closeResponse(t, resp)

	if !errors.Is(err, ErrDestinationNotPublic) {
		t.Fatalf("expected private redirect to be blocked, got %v", err)
	}

	if count := roundTrips.Load(); count != 1 {
		t.Fatalf("round trips = %d, want 1", count)
	}
}

func TestClientStopsRedirectLoops(t *testing.T) {
	t.Parallel()

	client := NewClient(ClientOptions{
		BaseTransport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return redirectResponse(req, "/again"), nil
		}),
		Resolver:     staticResolver(publicTestIP),
		MaxRedirects: 2,
	})

	resp, err := client.Do(newGetRequest(t, "http://"+publicTestHost+"/start"))
	closeResponse(t, resp)

	if !errors.Is(err, ErrTooManyRedirects) {
		t.Fatalf("expected redirect limit error, got %v", err)
	}
}

func TestClientLimitsUnknownLengthResponses(t *testing.T) {
	t.Parallel()

	client := NewClient(ClientOptions{
		BaseTransport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode:    http.StatusOK,
				Body:          io.NopCloser(strings.NewReader("12345")),
				ContentLength: -1,
				Header:        make(http.Header),
				Request:       req,
			}, nil
		}),
		Resolver:         staticResolver(publicTestIP),
		MaxResponseBytes: 4,
	})

	resp, err := client.Do(newGetRequest(t, "http://"+publicTestHost+"/large"))
	if err != nil {
		t.Fatalf("get limited response: %v", err)
	}
	defer closeResponse(t, resp)

	_, err = io.ReadAll(resp.Body)
	if !errors.Is(err, ErrResponseTooLarge) {
		t.Fatalf("expected response limit error, got %v", err)
	}
}

func staticResolver(addresses ...string) LookupIPAddrFunc {
	return LookupIPAddrFunc(func(_ context.Context, _ string) ([]net.IPAddr, error) {
		return ipAddrs(addresses...), nil
	})
}

func servePipeHTTP(conn net.Conn, expectedHost string, result chan<- error) {
	var resultErr error

	defer func() {
		result <- errors.Join(resultErr, conn.Close())
	}()

	req, err := http.ReadRequest(bufio.NewReader(conn))
	if err != nil {
		resultErr = err

		return
	}

	if req.Host != expectedHost {
		resultErr = errors.Join(
			fmt.Errorf("%w: got %q, want %q", errUnexpectedHostHeader, req.Host, expectedHost),
			req.Body.Close(),
		)

		return
	}

	_, writeErr := io.WriteString(conn, "HTTP/1.1 200 OK\r\nContent-Length: 2\r\nConnection: close\r\n\r\nok")
	resultErr = errors.Join(req.Body.Close(), writeErr)
}

func redirectResponse(req *http.Request, location string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusFound,
		Header:     http.Header{"Location": []string{location}},
		Body:       http.NoBody,
		Request:    req,
	}
}

func closeResponse(t *testing.T, resp *http.Response) {
	t.Helper()

	if resp != nil && resp.Body != nil {
		err := resp.Body.Close()
		if err != nil {
			t.Errorf("close response: %v", err)
		}
	}
}

func newGetRequest(t *testing.T, rawURL string) *http.Request {
	t.Helper()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, rawURL, http.NoBody)
	if err != nil {
		t.Fatalf("build GET request: %v", err)
	}

	return req
}

func newTLSTestServer(t *testing.T, host string, handler http.Handler) (*httptest.Server, *http.Transport) {
	t.Helper()

	certificate, root := newTestCertificate(t, host)
	server := httptest.NewUnstartedServer(handler)
	server.TLS = &tls.Config{
		Certificates: []tls.Certificate{certificate},
		MinVersion:   tls.VersionTLS12,
	}
	server.StartTLS()
	t.Cleanup(server.Close)

	return server, &http.Transport{TLSClientConfig: &tls.Config{
		RootCAs:    root,
		MinVersion: tls.VersionTLS12,
	}}
}

func newTestCertificate(t *testing.T, host string) (tls.Certificate, *x509.CertPool) {
	t.Helper()

	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate test TLS key: %v", err)
	}

	now := time.Now()
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(time.Hour),
		DNSNames:              []string{host},
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IsCA:                  true,
		BasicConstraintsValid: true,
	}

	der, err := x509.CreateCertificate(rand.Reader, template, template, &privateKey.PublicKey, privateKey)
	if err != nil {
		t.Fatalf("create test TLS certificate: %v", err)
	}

	parsed, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse test TLS certificate: %v", err)
	}

	roots := x509.NewCertPool()
	roots.AddCert(parsed)

	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: privateKey}, roots
}
