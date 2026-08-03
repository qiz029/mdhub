package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"time"
)

const maxRemoteSourceRedirects = 5

var errRemoteSourceDisallowed = errors.New("remote source is not allowed")

type remoteSourceClient struct {
	http *http.Client
}

// newRemoteSourceClient owns the network safety policy for content fetched
// from user-supplied URLs. Content-specific adapters still parse feeds and
// PDFs, but they do not need to reproduce DNS, redirect, proxy, or timeout
// rules.
func newRemoteSourceClient(timeout time.Duration) *remoteSourceClient {
	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	// A process-level proxy could resolve the destination on our behalf and
	// bypass the address checks below, so remote source traffic never uses it.
	transport.Proxy = nil
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, fmt.Errorf("dial remote source: %w", err)
		}
		addresses, err := net.DefaultResolver.LookupIPAddr(ctx, host)
		if err != nil {
			return nil, fmt.Errorf("resolve remote source: %w", err)
		}
		for _, candidate := range addresses {
			if disallowedRemoteIP(candidate.IP) {
				continue
			}
			return dialer.DialContext(ctx, network, net.JoinHostPort(candidate.IP.String(), port))
		}
		return nil, errRemoteSourceDisallowed
	}
	return &remoteSourceClient{http: &http.Client{
		Transport: transport,
		Timeout:   timeout,
		CheckRedirect: func(request *http.Request, via []*http.Request) error {
			if len(via) >= maxRemoteSourceRedirects {
				return fmt.Errorf("remote source has too many redirects")
			}
			if err := validateRemoteSourceURL(request.URL); err != nil {
				return err
			}
			return nil
		},
	}}
}

func (c *remoteSourceClient) Do(request *http.Request) (*http.Response, error) {
	return c.http.Do(request)
}

func validateRemoteSourceURL(sourceURL *url.URL) error {
	if sourceURL == nil || sourceURL.Host == "" || sourceURL.User != nil ||
		(sourceURL.Scheme != "http" && sourceURL.Scheme != "https") {
		return errRemoteSourceDisallowed
	}
	return nil
}

func disallowedRemoteIP(ip net.IP) bool {
	return ip == nil || !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast()
}

func readRemoteSourceBody(reader io.Reader, limit int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("remote source exceeds %d bytes", limit)
	}
	return data, nil
}
