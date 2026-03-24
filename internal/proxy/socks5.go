package proxy

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"strconv"
)

type connectRequest struct {
	Host string
	Port uint16
}

func (r connectRequest) Address() string {
	return net.JoinHostPort(r.Host, strconv.Itoa(int(r.Port)))
}

func handshakeSOCKS5(conn net.Conn) (connectRequest, error) {
	var header [2]byte
	if _, err := io.ReadFull(conn, header[:]); err != nil {
		return connectRequest{}, err
	}
	if header[0] != 0x05 {
		return connectRequest{}, fmt.Errorf("unsupported SOCKS version %d", header[0])
	}

	methods := make([]byte, int(header[1]))
	if _, err := io.ReadFull(conn, methods); err != nil {
		return connectRequest{}, err
	}

	if _, err := conn.Write([]byte{0x05, 0x00}); err != nil {
		return connectRequest{}, err
	}

	var reqHeader [4]byte
	if _, err := io.ReadFull(conn, reqHeader[:]); err != nil {
		return connectRequest{}, err
	}
	if reqHeader[0] != 0x05 || reqHeader[1] != 0x01 {
		_, _ = conn.Write([]byte{0x05, 0x07, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
		return connectRequest{}, fmt.Errorf("unsupported command %d", reqHeader[1])
	}

	host, err := readSOCKSHost(conn, reqHeader[3])
	if err != nil {
		return connectRequest{}, err
	}

	var portBytes [2]byte
	if _, err := io.ReadFull(conn, portBytes[:]); err != nil {
		return connectRequest{}, err
	}
	port := binary.BigEndian.Uint16(portBytes[:])

	_, err = conn.Write([]byte{0x05, 0x00, 0x00, 0x01, 127, 0, 0, 1, 0x04, 0x38})
	if err != nil {
		return connectRequest{}, err
	}

	return connectRequest{Host: host, Port: port}, nil
}

func readSOCKSHost(conn net.Conn, atyp byte) (string, error) {
	switch atyp {
	case 0x01:
		var raw [4]byte
		if _, err := io.ReadFull(conn, raw[:]); err != nil {
			return "", err
		}
		return net.IP(raw[:]).String(), nil
	case 0x03:
		var size [1]byte
		if _, err := io.ReadFull(conn, size[:]); err != nil {
			return "", err
		}
		domain := make([]byte, int(size[0]))
		if _, err := io.ReadFull(conn, domain); err != nil {
			return "", err
		}
		return string(domain), nil
	case 0x04:
		var raw [16]byte
		if _, err := io.ReadFull(conn, raw[:]); err != nil {
			return "", err
		}
		return net.IP(raw[:]).String(), nil
	default:
		return "", fmt.Errorf("unsupported address type %d", atyp)
	}
}
