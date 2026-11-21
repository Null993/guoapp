//go:build !windows

package core

import "net/http"

func newHanxiaoquanMediaNetwork(_ *http.Transport, _ *proxyRouter, next idleClosingTransport) idleClosingTransport {
	return next
}
