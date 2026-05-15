// SPDX-License-Identifier: MIT

package tapo

type Session interface {
	Handshake(host, username, password string) error
	Request([]byte) (*UntypedResponse, error)
	Host() string
}
