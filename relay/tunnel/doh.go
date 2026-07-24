package tunnel

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"
)

type DoHResolver struct {
	client  *http.Client
	servers []string
	cache   sync.Map // string(dnsQueryWithoutID) -> dohCacheEntry
	logFn   func(string, ...any)
}

type dohCacheEntry struct {
	response []byte
	expires  time.Time
}

func NewDoHResolver(logFn func(string, ...any)) *DoHResolver {
	if logFn == nil {
		logFn = func(string, ...any) {}
	}
	return &DoHResolver{
		client: &http.Client{
			Timeout: 4 * time.Second,
			Transport: &http.Transport{
				MaxIdleConns:        50,
				IdleConnTimeout:     60 * time.Second,
				TLSHandshakeTimeout: 3 * time.Second,
			},
		},
		servers: []string{
			"https://1.1.1.1/dns-query",
			"https://dns.google/dns-query",
			"https://cloudflare-dns.com/dns-query",
		},
		logFn: logFn,
	}
}

func (d *DoHResolver) Resolve(ctx context.Context, dnsReq []byte) ([]byte, error) {
	if len(dnsReq) < 12 {
		return nil, fmt.Errorf("dns request too short")
	}

	cacheKey := string(dnsReq[2:])
	if val, ok := d.cache.Load(cacheKey); ok {
		entry := val.(dohCacheEntry)
		if time.Now().Before(entry.expires) {
			res := make([]byte, len(entry.response))
			copy(res, entry.response)
			res[0] = dnsReq[0]
			res[1] = dnsReq[1]
			return res, nil
		}
		d.cache.Delete(cacheKey)
	}

	var lastErr error
	for _, server := range d.servers {
		reqCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		req, err := http.NewRequestWithContext(reqCtx, "POST", server, bytes.NewReader(dnsReq))
		if err != nil {
			cancel()
			lastErr = err
			continue
		}
		req.Header.Set("Content-Type", "application/dns-message")
		req.Header.Set("Accept", "application/dns-message")

		resp, err := d.client.Do(req)
		if err != nil {
			cancel()
			lastErr = err
			continue
		}

		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		cancel()

		if err != nil || resp.StatusCode != http.StatusOK || len(body) < 12 {
			lastErr = fmt.Errorf("doh status %d err %v", resp.StatusCode, err)
			continue
		}

		body[0] = dnsReq[0]
		body[1] = dnsReq[1]

		d.cache.Store(cacheKey, dohCacheEntry{
			response: body,
			expires:  time.Now().Add(60 * time.Second),
		})

		return body, nil
	}

	return nil, fmt.Errorf("all DoH servers failed: %v", lastErr)
}
