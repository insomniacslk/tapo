// SPDX-License-Identifier: MIT

package tapo

import "net/netip"

type Session interface {
	Handshake() error
	Request([]byte) ([]byte, error)
	Addr() netip.Addr
}
