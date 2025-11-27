// SPDX-FileCopyrightText: 2022 k0s authors
// SPDX-License-Identifier: Apache-2.0

package net

import (
	"encoding"
	"errors"
	"fmt"
	"net"
	"strconv"

	"github.com/asaskevich/govalidator"
)

// HostPort represents a host and port combination. The port is not optional.
type HostPort struct {
	host string
	port uint16
}

func (h *HostPort) Host() string { return h.host }
func (h *HostPort) Port() uint16 { return h.port }

func NewHostPort(host string, port uint16) (*HostPort, error) {
	if err := validateHost(host); err != nil {
		return nil, err
	}
	if port == 0 {
		return nil, errors.New("port is zero")
	}

	return &HostPort{host, port}, nil
}

func ParseHostPort(hostPort string) (*HostPort, error) {
	return ParseHostPortWithDefault(hostPort, 0)
}

func ParseHostPortWithDefault(hostPort string, defaultPort uint16) (*HostPort, error) {
	if govalidator.IsIP(hostPort) {
		if defaultPort == 0 {
			return nil, errors.New("missing port in address")
		}
		return &HostPort{hostPort, defaultPort}, nil
	}

	var port uint16
	host, portStr, err := net.SplitHostPort(hostPort)
	if err != nil {
		addrErr := &net.AddrError{}
		if !errors.As(err, &addrErr) {
			return nil, err
		}

		if addrErr.Err == "missing port in address" {
			if defaultPort != 0 {
				host = addrErr.Addr
				port = defaultPort
			} else {
				if _, ok := unwrapIPv6Literal(addrErr.Addr); !ok {
					if err := validateHost(addrErr.Addr); err != nil {
						return nil, err
					}
				}
				return nil, errors.New(addrErr.Err)
			}
		} else {
			return nil, errors.New(addrErr.Err)
		}
	} else {
		parsed, err := strconv.ParseUint(portStr, 10, 16)
		if err != nil {
			switch {
			case errors.Is(err, strconv.ErrSyntax):
				err = fmt.Errorf("port is not a positive number: %q", portStr)
			case errors.Is(err, strconv.ErrRange):
				err = fmt.Errorf("port is out of range: %s", portStr)
			default:
				err = fmt.Errorf("invalid port: %q: %w", portStr, err)
			}

			return nil, err
		}
		port = uint16(parsed)
	}

	if literal, ok := unwrapIPv6Literal(host); ok {
		return &HostPort{literal, port}, nil
	}

	return NewHostPort(host, port)
}

func (h *HostPort) String() string {
	return net.JoinHostPort(h.host, strconv.FormatUint(uint64(h.port), 10))
}

var (
	_ encoding.TextMarshaler   = (*HostPort)(nil)
	_ encoding.TextUnmarshaler = (*HostPort)(nil)
)

// MarshalText implements [encoding.TextMarshaler].
func (h *HostPort) MarshalText() (text []byte, err error) {
	return []byte(h.String()), nil
}

// UnmarshalText implements [encoding.TextUnmarshaler].
func (h *HostPort) UnmarshalText(text []byte) error {
	parsed, err := ParseHostPort(string(text))
	if err != nil {
		return err
	}
	*h = *parsed
	return err
}

func unwrapIPv6Literal(host string) (string, bool) {
	if len := len(host); len > 2 && host[0] == '[' && host[len-1] == ']' {
		if host := host[1 : len-1]; govalidator.IsIPv6(host) {
			return host, true
		}
	}

	return host, false
}

func validateHost(host string) error {
	switch {
	case govalidator.IsIP(host):
		return nil
	case govalidator.IsDNSName(host):
		return nil
	default:
		return errors.New("host is neither an IP address nor a DNS name")
	}
}
