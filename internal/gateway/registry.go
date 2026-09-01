package gateway

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/Fuucodi0X/tailcat-preview/internal/protocol"
	"github.com/Fuucodi0X/tailcat-preview/internal/session"
	"github.com/tailscale/tailcat"
	"tailscale.com/types/key"
)

var ErrPreviewNotFound = errors.New("preview not found")

type Registry struct {
	mu        sync.RWMutex
	clientKey key.NodePrivate
	previews  map[string]*Preview
}

type Preview struct {
	protocol.Preview
	owner     string
	client    *tailcat.Client
	transport *http.Transport
	proxy     *httputil.ReverseProxy
}

func NewRegistry(clientKey key.NodePrivate) *Registry {
	return &Registry{
		clientKey: clientKey,
		previews:  make(map[string]*Preview),
	}
}

func (r *Registry) Upsert(owner string, spec protocol.Preview) error {
	if err := validatePreview(spec, time.Now()); err != nil {
		return err
	}
	if _, err := tailcat.ParseConnBlob(tailcat.ConnBlob(spec.ConnToken)); err != nil {
		return fmt.Errorf("invalid Tailcat connection token: %w", err)
	}

	p := newPreview(owner, spec, r.clientKey)
	r.mu.Lock()
	previous := r.previews[spec.ID]
	r.previews[spec.ID] = p
	r.mu.Unlock()
	if previous != nil {
		previous.close()
	}
	return nil
}

func (r *Registry) Get(id string, now time.Time) (*Preview, error) {
	r.mu.RLock()
	p := r.previews[id]
	r.mu.RUnlock()
	if p == nil {
		return nil, ErrPreviewNotFound
	}
	if !now.Before(p.ExpiresAt) {
		r.Remove(id)
		return nil, ErrPreviewNotFound
	}
	return p, nil
}

func (r *Registry) Remove(id string) {
	r.mu.Lock()
	p := r.previews[id]
	delete(r.previews, id)
	r.mu.Unlock()
	if p != nil {
		p.close()
	}
}

func (r *Registry) RemoveOwner(owner string) {
	var removed []*Preview
	r.mu.Lock()
	for id, p := range r.previews {
		if p.owner == owner {
			removed = append(removed, p)
			delete(r.previews, id)
		}
	}
	r.mu.Unlock()
	for _, p := range removed {
		p.close()
	}
}

func (r *Registry) Close() {
	r.mu.Lock()
	previews := r.previews
	r.previews = make(map[string]*Preview)
	r.mu.Unlock()
	for _, p := range previews {
		p.close()
	}
}

func (p *Preview) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	p.proxy.ServeHTTP(w, req)
}

func (p *Preview) close() {
	p.transport.CloseIdleConnections()
	_ = p.client.Close()
}

func newPreview(owner string, spec protocol.Preview, clientKey key.NodePrivate) *Preview {
	client := &tailcat.Client{
		Server: tailcat.ConnBlob(spec.ConnToken),
		Key:    clientKey,
		Logf:   func(string, ...any) {},
	}
	transport := &http.Transport{
		Proxy:                 nil,
		ForceAttemptHTTP2:     false,
		MaxIdleConns:          64,
		MaxIdleConnsPerHost:   32,
		IdleConnTimeout:       90 * time.Second,
		ResponseHeaderTimeout: 60 * time.Second,
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return client.DialTCPPort(ctx, spec.Port)
		},
	}

	p := &Preview{
		Preview:   spec,
		owner:     owner,
		client:    client,
		transport: transport,
	}
	p.proxy = &httputil.ReverseProxy{
		Rewrite: func(proxyReq *httputil.ProxyRequest) {
			proxyReq.SetURL(&url.URL{Scheme: "http", Host: "tailcat-preview.internal"})
			proxyReq.SetXForwarded()
			proxyReq.Out.Host = fmt.Sprintf("localhost:%d", spec.Port)
			stripGatewayCookie(proxyReq.Out)
		},
		Transport:     transport,
		FlushInterval: -1,
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, err error) {
			http.Error(w, "The laptop preview is unavailable. Check that the dev server and expose command are still running.", http.StatusBadGateway)
		},
		ModifyResponse: rewriteLocalRedirect,
	}
	return p
}

func rewriteLocalRedirect(resp *http.Response) error {
	stripGatewaySetCookie(resp.Header)
	location := resp.Header.Get("Location")
	if location == "" {
		return nil
	}
	u, err := url.Parse(location)
	if err != nil || !u.IsAbs() {
		return nil
	}
	host := strings.ToLower(u.Hostname())
	if host != "localhost" && host != "127.0.0.1" && host != "::1" {
		return nil
	}
	u.Scheme = firstHeaderValue(resp.Request.Header.Get("X-Forwarded-Proto"), "https")
	u.Host = resp.Request.Header.Get("X-Forwarded-Host")
	if u.Host == "" {
		return nil
	}
	resp.Header.Set("Location", u.String())
	return nil
}

func stripGatewayCookie(req *http.Request) {
	cookies := req.Cookies()
	req.Header.Del("Cookie")
	for _, cookie := range cookies {
		if cookie.Name != session.CookieName {
			req.AddCookie(cookie)
		}
	}
}

func stripGatewaySetCookie(header http.Header) {
	values := header.Values("Set-Cookie")
	header.Del("Set-Cookie")
	for _, value := range values {
		name, _, _ := strings.Cut(value, "=")
		if strings.TrimSpace(name) != session.CookieName {
			header.Add("Set-Cookie", value)
		}
	}
}

func firstHeaderValue(value, fallback string) string {
	if first, _, ok := strings.Cut(value, ","); ok {
		value = first
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return value
}

func validatePreview(p protocol.Preview, now time.Time) error {
	if len(p.ID) < 8 || len(p.ID) > 64 {
		return errors.New("preview ID must be between 8 and 64 characters")
	}
	for _, r := range p.ID {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '-' && r != '_' {
			return errors.New("preview ID contains an invalid character")
		}
	}
	if p.Name == "" || len(p.Name) > 100 {
		return errors.New("preview name must be between 1 and 100 characters")
	}
	if p.Port == 0 {
		return errors.New("preview port is required")
	}
	if len(p.ConnToken) < 10 || len(p.ConnToken) > 16*1024 {
		return errors.New("Tailcat connection token has an invalid length")
	}
	if len(p.AccessHash) != 43 {
		return errors.New("preview access hash has an invalid length")
	}
	if !p.ExpiresAt.After(now) {
		return errors.New("preview expiry must be in the future")
	}
	if p.ExpiresAt.After(now.Add(24 * time.Hour)) {
		return errors.New("preview expiry cannot be more than 24 hours away")
	}
	return nil
}
