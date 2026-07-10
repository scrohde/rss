package outbound

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const defaultMaxRedirects = 5

var (
	// ErrTooManyRedirects indicates that an outbound request exceeded its redirect budget.
	ErrTooManyRedirects = errors.New("too many outbound redirects")
	// ErrResponseTooLarge indicates that an outbound response exceeded its configured byte limit.
	ErrResponseTooLarge = errors.New("outbound response too large")
	// ErrUnpinnedDial indicates that a transport attempted a connection without a validated destination.
	ErrUnpinnedDial = errors.New("outbound connection is not pinned")
)

// DialContextFunc opens a network connection for an approved address.
type DialContextFunc func(context.Context, string, string) (net.Conn, error)

// ClientOptions configures a policy-enforcing outbound HTTP client.
type ClientOptions struct {
	Resolver         Resolver
	BaseTransport    http.RoundTripper
	DialContext      DialContextFunc
	Timeout          time.Duration
	MaxResponseBytes int64
	MaxRedirects     int
}

type policyTransport struct {
	base             http.RoundTripper
	resolver         Resolver
	dialContext      DialContextFunc
	maxResponseBytes int64
}

type approvedContextKey struct{}

type limitedReadCloser struct {
	body      io.ReadCloser
	remaining int64
}

// NewClient returns an HTTP client that validates, resolves, and pins every request and redirect destination.
func NewClient(options ClientOptions) *http.Client {
	transport := newPolicyTransport(options)

	redirectLimit := options.MaxRedirects
	if redirectLimit <= 0 {
		redirectLimit = defaultMaxRedirects
	}

	client := new(http.Client)
	client.Transport = transport
	client.Timeout = options.Timeout
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= redirectLimit {
			return fmt.Errorf("%w: limit %d", ErrTooManyRedirects, redirectLimit)
		}

		return ValidateURL(req.URL)
	}

	return client
}

func newPolicyTransport(options ClientOptions) *policyTransport {
	resolver := options.Resolver
	if resolver == nil {
		resolver = net.DefaultResolver
	}

	base := options.BaseTransport
	if base == nil {
		base = http.DefaultTransport
	}

	transport := &policyTransport{
		base:             base,
		resolver:         resolver,
		dialContext:      options.DialContext,
		maxResponseBytes: options.MaxResponseBytes,
	}
	if httpTransport, ok := base.(*http.Transport); ok {
		transport.base = transport.secureHTTPTransport(httpTransport)
	}

	return transport
}

// RoundTrip enforces URL and DNS policy before delegating a request.
func (t *policyTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req == nil {
		return nil, ErrURLNotAllowed
	}

	approved, err := resolveDestination(req.Context(), req.URL, t.resolver)
	if err != nil {
		return nil, err
	}

	safeRequest := cloneApprovedRequest(req, approved)

	resp, err := t.base.RoundTrip(safeRequest)
	if err != nil {
		return nil, fmt.Errorf("outbound round trip: %w", err)
	}

	if t.maxResponseBytes <= 0 || resp.Body == nil {
		return resp, nil
	}

	if resp.ContentLength > t.maxResponseBytes {
		return nil, errors.Join(ErrResponseTooLarge, resp.Body.Close())
	}

	resp.Body = &limitedReadCloser{body: resp.Body, remaining: t.maxResponseBytes}

	return resp, nil
}

func (t *policyTransport) secureHTTPTransport(base *http.Transport) *http.Transport {
	result := base.Clone()

	dialContext := t.dialContext
	if dialContext == nil {
		dialContext = result.DialContext
	}

	if dialContext == nil {
		dialer := new(net.Dialer)
		dialContext = dialer.DialContext
	}

	t.dialContext = dialContext
	result.Proxy = nil
	result.DialContext = t.dialApproved
	result.DialTLSContext = nil
	//nolint:staticcheck // Clear the deprecated hook so it cannot bypass the pinned DialContext.
	result.DialTLS = nil

	return result
}

func cloneApprovedRequest(req *http.Request, approved destination) *http.Request {
	ctx := context.WithValue(req.Context(), approvedContextKey{}, approved)
	result := req.Clone(ctx)
	clonedURL := new(url.URL)
	*clonedURL = *req.URL
	clonedURL.Scheme = approved.scheme

	clonedURL.Host = approved.host
	if strings.Contains(approved.host, ":") {
		clonedURL.Host = "[" + approved.host + "]"
	}

	result.URL = clonedURL
	result.Host = ""

	return result
}

func (t *policyTransport) dialApproved(ctx context.Context, network, _ string) (net.Conn, error) {
	approved, ok := ctx.Value(approvedContextKey{}).(destination)
	if !ok || len(approved.addresses) == 0 || t.dialContext == nil {
		return nil, ErrUnpinnedDial
	}

	var dialErr error

	for _, addr := range approved.addresses {
		target := net.JoinHostPort(addr.String(), approved.port)

		conn, err := t.dialContext(ctx, network, target)
		if err == nil {
			return conn, nil
		}

		dialErr = errors.Join(dialErr, fmt.Errorf("dial approved address %s: %w", addr, err))
	}

	return nil, dialErr
}

// Read exposes at most the configured response body limit and errors if more data exists.
func (r *limitedReadCloser) Read(buffer []byte) (int, error) {
	if r.remaining > 0 {
		if int64(len(buffer)) > r.remaining {
			buffer = buffer[:r.remaining]
		}

		read, err := r.body.Read(buffer)
		r.remaining -= int64(read)

		return read, wrappedReadError(err)
	}

	var probe [1]byte

	read, err := r.body.Read(probe[:])
	if read > 0 {
		return 0, ErrResponseTooLarge
	}

	return 0, wrappedReadError(err)
}

// Close closes the underlying response body.
func (r *limitedReadCloser) Close() error {
	err := r.body.Close()
	if err != nil {
		return fmt.Errorf("close limited outbound response: %w", err)
	}

	return nil
}

func wrappedReadError(err error) error {
	if err == nil || errors.Is(err, io.EOF) {
		return err
	}

	return fmt.Errorf("read limited outbound response: %w", err)
}
