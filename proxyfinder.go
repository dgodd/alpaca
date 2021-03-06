package main

import (
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
)

// The DefaultClient in net/http uses the proxy specified in the http(s)_proxy environment variable,
// which could be pointing at this instance of alpaca. When fetching the PAC file, we always use a
// client that goes directly to the server, rather than via a proxy.
var noProxyClient = &http.Client{Transport: &http.Transport{Proxy: nil}}

type ProxyFinder struct {
	pacURL     string
	pacRunner  *PACRunner
	netMonitor *NetMonitor
	online     bool
}

func NewProxyFinder(pacURL string) *ProxyFinder {
	return newProxyFinder(pacURL, net.InterfaceAddrs)
}

func newProxyFinder(pacURL string, getAddrs addressProvider) *ProxyFinder {
	pf := &ProxyFinder{pacURL: pacURL, netMonitor: NewNetMonitor(getAddrs)}
	pf.downloadPACFile()
	return pf
}

func (pf *ProxyFinder) findProxyForRequest(req *http.Request) (*url.URL, error) {
	// TODO: this is probably not thread-safe; put a lock around it
	if pf.netMonitor.AddrsChanged() {
		pf.downloadPACFile()
	}
	if !pf.online {
		return nil, nil
	}
	s, err := pf.pacRunner.FindProxyForURL(req.URL)
	if err != nil {
		return nil, err
	}
	id := req.Context().Value("id")
	log.Printf("[%d] %s %s via %q", id, req.Method, req.URL, s)
	ss := strings.Split(s, ";")
	if len(ss) > 1 {
		log.Printf("[%d] Warning: ignoring all but first proxy in %q", id, s)
	}
	trimmed := strings.TrimSpace(ss[0])
	if trimmed == "DIRECT" {
		return nil, nil
	}
	var host string
	n, err := fmt.Sscanf(trimmed, "PROXY %s", &host)
	if err == nil && n == 1 {
		// The specified proxy should contain both a host and a port, but if for some reason
		// it doesn't, assume port 80. This needs to be made explicit, as it eventually gets
		// passed to net.Dial, which also requires a port.
		proxy := &url.URL{Host: host}
		if proxy.Port() == "" {
			proxy.Host = net.JoinHostPort(host, "80")
		}
		return proxy, nil
	}
	n, err = fmt.Sscanf(trimmed, "SOCKS %s", &host)
	if err == nil && n == 1 {
		log.Printf("[%d] Warning: ignoring SOCKS proxy %q", id, host)
		return nil, nil
	}
	log.Printf("[%d] Couldn't parse PAC response %q", id, s)
	return nil, err
}

func (pf *ProxyFinder) downloadPACFile() {
	resp, err := noProxyClient.Get(pf.pacURL)
	if err != nil {
		log.Printf("Error downloading PAC file: %q\n", err)
		pf.online = false
		return
	}
	defer resp.Body.Close()
	log.Printf("GET %q returned %q\n", pf.pacURL, resp.Status)
	if resp.StatusCode != http.StatusOK {
		pf.online = false
		return
	}
	pf.pacRunner, err = NewPACRunner(resp.Body)
	if err != nil {
		log.Printf("Error creating new PAC runner: %q\n", err)
		pf.online = false
		return
	}
	pf.online = true
	return
}
