package core

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"slices"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var syntheticIPv4 = netip.MustParsePrefix("198.18.0.0/15")

type hanxiaoquanMediaNetwork struct {
	next   idleClosingTransport
	lan    idleClosingTransport
	router *proxyRouter
	lookup func(context.Context, string, string) ([]netip.Addr, error)
}

func newHanxiaoquanMediaNetwork(base *http.Transport, router *proxyRouter, next idleClosingTransport) idleClosingTransport {
	lan := base.Clone()
	lan.Proxy = nil
	lan.DialContext = dialHanxiaoquanLAN
	lan.ResponseHeaderTimeout = 6 * time.Second
	lan.TLSHandshakeTimeout = 4 * time.Second
	return &hanxiaoquanMediaNetwork{next: next, lan: lan, router: router, lookup: net.DefaultResolver.LookupNetIP}
}

func (transport *hanxiaoquanMediaNetwork) RoundTrip(request *http.Request) (*http.Response, error) {
	if request.Context().Value(hanxiaoquanMediaNetworkKey{}) != true || request.URL.Scheme != "https" ||
		(request.Method != http.MethodGet && request.Method != http.MethodHead) {
		return transport.next.RoundTrip(request)
	}
	transport.router.mu.RLock()
	mode := transport.router.mode
	transport.router.mu.RUnlock()
	if mode == "manual" {
		return transport.next.RoundTrip(request)
	}
	lookupContext, cancel := context.WithTimeout(request.Context(), 600*time.Millisecond)
	addresses, err := transport.lookup(lookupContext, "ip4", request.URL.Hostname())
	cancel()
	if err != nil || len(addresses) == 0 {
		return transport.next.RoundTrip(request)
	}
	for _, address := range addresses {
		if !syntheticIPv4.Contains(address.Unmap()) {
			return transport.next.RoundTrip(request)
		}
	}
	response, lanErr := transport.lan.RoundTrip(request)
	if lanErr == nil || request.Context().Err() != nil {
		return response, lanErr
	}
	response, err = transport.next.RoundTrip(request)
	if err != nil {
		return nil, errors.Join(fmt.Errorf("韩小圈网卡直连失败: %w", lanErr), err)
	}
	return response, nil
}

func (transport *hanxiaoquanMediaNetwork) CloseIdleConnections() {
	transport.lan.CloseIdleConnections()
	transport.next.CloseIdleConnections()
}

type hanxiaoquanLANAdapter struct {
	address net.IP
	dns     []string
	metric  uint32
}

func hanxiaoquanLANAdapters() ([]hanxiaoquanLANAdapter, error) {
	size := uint32(15 << 10)
	for range 3 {
		buffer := make([]byte, size)
		first := (*windows.IpAdapterAddresses)(unsafe.Pointer(&buffer[0]))
		flags := uint32(windows.GAA_FLAG_INCLUDE_GATEWAYS | windows.GAA_FLAG_SKIP_ANYCAST | windows.GAA_FLAG_SKIP_MULTICAST)
		err := windows.GetAdaptersAddresses(windows.AF_INET, flags, 0, first, &size)
		if err == windows.ERROR_BUFFER_OVERFLOW && size > uint32(len(buffer)) && size <= 1<<20 {
			continue
		}
		if err != nil {
			return nil, err
		}
		var adapters []hanxiaoquanLANAdapter
		for adapter := first; adapter != nil; adapter = adapter.Next {
			if adapter.OperStatus != windows.IfOperStatusUp || adapter.FirstGatewayAddress == nil ||
				(adapter.IfType != windows.IF_TYPE_ETHERNET_CSMACD && adapter.IfType != windows.IF_TYPE_IEEE80211) {
				continue
			}
			candidate := hanxiaoquanLANAdapter{metric: adapter.Ipv4Metric}
			for address := adapter.FirstUnicastAddress; address != nil; address = address.Next {
				if ip := address.Address.IP().To4(); usableLANAddress(ip) {
					candidate.address = slices.Clone(ip)
					break
				}
			}
			for server := adapter.FirstDnsServerAddress; server != nil; server = server.Next {
				if ip := server.Address.IP().To4(); usableLANAddress(ip) {
					candidate.dns = append(candidate.dns, ip.String())
				}
			}
			if candidate.address != nil && len(candidate.dns) != 0 {
				adapters = append(adapters, candidate)
			}
		}
		slices.SortStableFunc(adapters, func(a, b hanxiaoquanLANAdapter) int {
			return int(a.metric) - int(b.metric)
		})
		return adapters, nil
	}
	return nil, errors.New("无法读取 Windows 网络适配器")
}

func usableLANAddress(ip net.IP) bool {
	address, ok := netip.AddrFromSlice(ip)
	return ok && address.IsGlobalUnicast() && !syntheticIPv4.Contains(address.Unmap())
}

func dialHanxiaoquanLAN(ctx context.Context, _, target string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(target)
	if err != nil {
		return nil, err
	}
	adapters, err := hanxiaoquanLANAdapters()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	lastErr := errors.New("Windows 网卡没有提供可用的媒体地址")
	for _, adapter := range adapters {
		for _, server := range adapter.dns {
			dns := net.Resolver{PreferGo: true, Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
				dial := net.Dialer{LocalAddr: &net.UDPAddr{IP: adapter.address}}
				if network == "tcp" {
					dial.LocalAddr = &net.TCPAddr{IP: adapter.address}
				}
				return dial.DialContext(ctx, network, net.JoinHostPort(server, "53"))
			}}
			work, stop := context.WithTimeout(ctx, 1200*time.Millisecond)
			ips, lookupErr := dns.LookupNetIP(work, "ip4", host+".")
			stop()
			if lookupErr != nil {
				lastErr = lookupErr
				continue
			}
			for _, ip := range ips[:min(4, len(ips))] {
				address := ip.Unmap().String()
				if validateCDNAddresses([]string{address}) != nil {
					continue
				}
				dial := net.Dialer{LocalAddr: &net.TCPAddr{IP: adapter.address}, Timeout: 1200 * time.Millisecond, KeepAlive: 30 * time.Second}
				conn, dialErr := dial.DialContext(ctx, "tcp4", net.JoinHostPort(address, port))
				if dialErr == nil {
					return conn, nil
				}
				lastErr = dialErr
			}
		}
	}
	return nil, lastErr
}
