package core

import (
	"net/http"
	"crypto/tls"
	"fmt"
	"net/url"
)

type Updater struct {
	proxy_url *url.URL
}

func (T Updater) http_get(target string) (*http.Response, error) {
	var transport http.Transport

	// Harvest proxy settings from admin.py.
	if T.proxy_url != nil {
		transport.Proxy = http.ProxyURL(T.proxy_url)
	}

	transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	transport.DisableKeepAlives = true

	req, err := http.NewRequest("GET", target, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("User-Agent", fmt.Sprintf("kitebroker"))

	client := &http.Client{Transport: &transport, Timeout: 0}
	return client.Do(req)
}

func KBUpdateSelf(proxy string) (err error) {
	return nil
}