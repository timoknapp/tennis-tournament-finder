package httpclient

import (
	"net"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"
)

// Default timeout values. Every outbound request made by the backend is bounded
// so a slow or stalled upstream can never block a goroutine indefinitely.
const (
	DefaultTimeout               = 20 * time.Second
	DefaultDialTimeout           = 5 * time.Second
	DefaultTLSHandshakeTimeout   = 5 * time.Second
	DefaultResponseHeaderTimeout = 15 * time.Second
	DefaultIdleConnTimeout       = 90 * time.Second

	// DefaultUserAgent identifies this application to upstream services.
	// Several public APIs (most notably Nominatim) require a descriptive
	// User-Agent with a way to contact the operator.
	DefaultUserAgent = "TennisTournamentFinder/1.0 (+https://github.com/timoknapp/tennis-tournament-finder)"
)

var (
	federationOnce   sync.Once
	federationClient *http.Client

	geocodingOnce   sync.Once
	geocodingClient *http.Client
)

// New builds an HTTP client with bounded timeouts at every stage of the
// request lifecycle.
func New(timeout time.Duration) *http.Client {
	transport := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   DefaultDialTimeout,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		TLSHandshakeTimeout:   DefaultTLSHandshakeTimeout,
		ResponseHeaderTimeout: DefaultResponseHeaderTimeout,
		ExpectContinueTimeout: 1 * time.Second,
		IdleConnTimeout:       DefaultIdleConnTimeout,
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   10,
	}

	return &http.Client{
		Timeout:   timeout,
		Transport: transport,
	}
}

// Federation returns the shared client used for federation scraping requests.
func Federation() *http.Client {
	federationOnce.Do(func() {
		federationClient = New(durationFromEnv("TTF_HTTP_TIMEOUT_SECONDS", DefaultTimeout))
	})
	return federationClient
}

// Geocoding returns the shared client used for geocoding requests.
func Geocoding() *http.Client {
	geocodingOnce.Do(func() {
		geocodingClient = New(durationFromEnv("TTF_GEOCODING_TIMEOUT_SECONDS", DefaultTimeout))
	})
	return geocodingClient
}

// UserAgent returns the User-Agent sent with outbound requests. It can be
// overridden with TTF_USER_AGENT so operators of a fork can supply their own
// contact details.
func UserAgent() string {
	if ua := os.Getenv("TTF_USER_AGENT"); ua != "" {
		return ua
	}
	return DefaultUserAgent
}

// ApplyDefaultHeaders sets headers that should accompany every outbound request.
func ApplyDefaultHeaders(req *http.Request) {
	if req.Header.Get("User-Agent") == "" {
		req.Header.Set("User-Agent", UserAgent())
	}
	if req.Header.Get("Accept-Language") == "" {
		req.Header.Set("Accept-Language", "de-DE,de;q=0.9")
	}
}

func durationFromEnv(key string, fallback time.Duration) time.Duration {
	raw := os.Getenv(key)
	if raw == "" {
		return fallback
	}

	seconds, err := strconv.Atoi(raw)
	if err != nil || seconds <= 0 {
		return fallback
	}

	return time.Duration(seconds) * time.Second
}
