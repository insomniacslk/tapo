// SPDX-License-Identifier: MIT

package tapo

import "net/netip"

type Session interface {
	Handshake(addr netip.Addr, username, password string) error
	Request([]byte) (*UntypedResponse, error)
	Addr() netip.Addr
}
