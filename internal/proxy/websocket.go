package proxy

import (
	"bufio"
	"crypto/rand"
	"crypto/sha1"
	"crypto/tls"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

const (
	wsOpcodeBinary = 0x2
	wsOpcodeClose  = 0x8
	wsOpcodePing   = 0x9
	wsOpcodePong   = 0xA
)

type wsConn struct {
	conn net.Conn
	r    *bufio.Reader
	mu   sync.Mutex
}

func dialTelegramWS(dc byte) (*wsConn, error) {
	var errs []string
	for _, host := range telegramWSEndpoints(dc) {
		ws, err := dialSingleTelegramWS(host)
		if err == nil {
			return ws, nil
		}
		errs = append(errs, err.Error())
	}
	if len(errs) == 0 {
		return nil, fmt.Errorf("no websocket endpoints for dc %d", dc)
	}
	return nil, fmt.Errorf("all websocket endpoints failed for dc %d: %s", dc, strings.Join(errs, " | "))
}

func dialSingleTelegramWS(host string) (*wsConn, error) {
	rawConn, err := dialTLSPreferIPv4(host)
	if err != nil {
		return nil, fmt.Errorf("dial websocket host %s: %w", host, err)
	}

	ws := &wsConn{
		conn: rawConn,
		r:    bufio.NewReader(rawConn),
	}

	if err := ws.handshake(host); err != nil {
		_ = rawConn.Close()
		return nil, fmt.Errorf("handshake %s: %w", host, err)
	}

	return ws, nil
}

func telegramWSEndpoints(dc byte) []string {
	switch dc {
	case 1:
		return []string{"pluto.web.telegram.org"}
	case 2:
		return []string{"venus.web.telegram.org"}
	case 3:
		return []string{"aurora.web.telegram.org"}
	case 4:
		return []string{"vesta.web.telegram.org"}
	case 5:
		return []string{"flora.web.telegram.org"}
	default:
		return []string{"venus.web.telegram.org"}
	}
}

func dialTLSPreferIPv4(host string) (net.Conn, error) {
	if proxyURL, ok := wsProxyFromEnv(); ok {
		conn, err := dialTLSViaHTTPProxy(host, proxyURL)
		if err == nil {
			return conn, nil
		}
		return nil, err
	}

	dialer := &net.Dialer{Timeout: 5 * time.Second}
	ips, err := net.LookupIP(host)
	if err == nil {
		var lastErr error
		for _, ip := range ips {
			if v4 := ip.To4(); v4 != nil {
				conn, dialErr := tls.DialWithDialer(
					dialer,
					"tcp4",
					net.JoinHostPort(v4.String(), "443"),
					&tls.Config{ServerName: host, MinVersion: tls.VersionTLS12},
				)
				if dialErr == nil {
					return conn, nil
				}
				lastErr = dialErr
			}
		}
		if lastErr != nil {
			return nil, lastErr
		}
	}

	return tls.DialWithDialer(
		dialer,
		"tcp",
		net.JoinHostPort(host, "443"),
		&tls.Config{ServerName: host, MinVersion: tls.VersionTLS12},
	)
}

func wsProxyFromEnv() (*url.URL, bool) {
	for _, key := range []string{"HTTPS_PROXY", "https_proxy", "ALL_PROXY", "all_proxy"} {
		raw := strings.TrimSpace(os.Getenv(key))
		if raw == "" {
			continue
		}
		u, err := url.Parse(raw)
		if err != nil {
			continue
		}
		if u.Scheme == "http" || u.Scheme == "https" {
			return u, true
		}
	}
	return nil, false
}

func dialTLSViaHTTPProxy(host string, proxyURL *url.URL) (net.Conn, error) {
	proxyHost := proxyURL.Host
	if !strings.Contains(proxyHost, ":") {
		switch proxyURL.Scheme {
		case "https":
			proxyHost = net.JoinHostPort(proxyHost, "443")
		default:
			proxyHost = net.JoinHostPort(proxyHost, "80")
		}
	}

	dialer := &net.Dialer{Timeout: 5 * time.Second}
	var conn net.Conn
	var err error

	if proxyURL.Scheme == "https" {
		conn, err = tls.DialWithDialer(
			dialer,
			"tcp",
			proxyHost,
			&tls.Config{ServerName: proxyURL.Hostname(), MinVersion: tls.VersionTLS12},
		)
	} else {
		conn, err = dialer.Dial("tcp", proxyHost)
	}
	if err != nil {
		return nil, fmt.Errorf("dial proxy %s: %w", proxyHost, err)
	}

	connectReq := &http.Request{
		Method: http.MethodConnect,
		URL:    &url.URL{Opaque: net.JoinHostPort(host, "443")},
		Host:   net.JoinHostPort(host, "443"),
		Header: make(http.Header),
	}
	if proxyURL.User != nil {
		user := proxyURL.User.Username()
		pass, _ := proxyURL.User.Password()
		basic := base64.StdEncoding.EncodeToString([]byte(user + ":" + pass))
		connectReq.Header.Set("Proxy-Authorization", "Basic "+basic)
	}

	if err := connectReq.Write(conn); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("write proxy connect request: %w", err)
	}

	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, connectReq)
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("read proxy connect response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		_ = conn.Close()
		return nil, fmt.Errorf("proxy connect status: %s", resp.Status)
	}

	tlsConn := tls.Client(&bufferedConn{Conn: conn, r: br}, &tls.Config{
		ServerName: host,
		MinVersion: tls.VersionTLS12,
	})
	if err := tlsConn.Handshake(); err != nil {
		_ = resp.Body.Close()
		_ = tlsConn.Close()
		return nil, fmt.Errorf("tls handshake via proxy: %w", err)
	}

	return tlsConn, nil
}

type bufferedConn struct {
	net.Conn
	r *bufio.Reader
}

func (c *bufferedConn) Read(p []byte) (int, error) {
	return c.r.Read(p)
}

func (w *wsConn) handshake(host string) error {
	keyRaw := make([]byte, 16)
	if _, err := rand.Read(keyRaw); err != nil {
		return fmt.Errorf("websocket key: %w", err)
	}
	key := base64.StdEncoding.EncodeToString(keyRaw)

	req := fmt.Sprintf(
		"GET /apiws HTTP/1.1\r\nHost: %s\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Key: %s\r\nSec-WebSocket-Version: 13\r\nSec-WebSocket-Protocol: binary\r\n\r\n",
		host,
		key,
	)
	if _, err := io.WriteString(w.conn, req); err != nil {
		return fmt.Errorf("write websocket handshake: %w", err)
	}

	resp, err := http.ReadResponse(w.r, &http.Request{Method: http.MethodGet})
	if err != nil {
		return fmt.Errorf("read websocket handshake: %w", err)
	}
	if resp.StatusCode != http.StatusSwitchingProtocols {
		_ = resp.Body.Close()
		return fmt.Errorf("websocket handshake status: %s", resp.Status)
	}

	expected := wsAccept(key)
	if resp.Header.Get("Sec-WebSocket-Accept") != expected {
		return fmt.Errorf("invalid websocket accept header")
	}

	return nil
}

func wsAccept(key string) string {
	sum := sha1.Sum([]byte(key + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
	return base64.StdEncoding.EncodeToString(sum[:])
}

func (w *wsConn) WriteBinary(payload []byte) error {
	return w.WriteControl(wsOpcodeBinary, payload)
}

func (w *wsConn) WriteControl(opcode byte, payload []byte) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return writeMaskedFrame(w.conn, opcode, payload)
}

func (w *wsConn) ReadFrame() (byte, []byte, error) {
	var header [2]byte
	if _, err := io.ReadFull(w.r, header[:]); err != nil {
		return 0, nil, err
	}

	fin := header[0]&0x80 != 0
	opcode := header[0] & 0x0F
	masked := header[1]&0x80 != 0
	length, err := wsPayloadLength(w.r, header[1]&0x7F)
	if err != nil {
		return 0, nil, err
	}

	var maskKey [4]byte
	if masked {
		if _, err := io.ReadFull(w.r, maskKey[:]); err != nil {
			return 0, nil, err
		}
	}

	payload := make([]byte, length)
	if _, err := io.ReadFull(w.r, payload); err != nil {
		return 0, nil, err
	}

	if masked {
		applyMask(payload, maskKey)
	}
	if !fin {
		return 0, nil, fmt.Errorf("fragmented websocket frames are not supported")
	}

	return opcode, payload, nil
}

func (w *wsConn) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	_ = writeMaskedFrame(w.conn, wsOpcodeClose, nil)
	return w.conn.Close()
}

func writeMaskedFrame(dst io.Writer, opcode byte, payload []byte) error {
	var header [14]byte
	header[0] = 0x80 | opcode

	pos := 2
	payloadLen := len(payload)
	switch {
	case payloadLen <= 125:
		header[1] = 0x80 | byte(payloadLen)
	case payloadLen <= 65535:
		header[1] = 0x80 | 126
		binary.BigEndian.PutUint16(header[2:4], uint16(payloadLen))
		pos = 4
	default:
		header[1] = 0x80 | 127
		binary.BigEndian.PutUint64(header[2:10], uint64(payloadLen))
		pos = 10
	}

	mask := header[pos : pos+4]
	if _, err := rand.Read(mask); err != nil {
		return err
	}
	pos += 4

	frame := make([]byte, pos+payloadLen)
	copy(frame[:pos], header[:pos])
	copy(frame[pos:], payload)
	applyMask(frame[pos:], [4]byte{mask[0], mask[1], mask[2], mask[3]})

	_, err := dst.Write(frame)
	return err
}

func wsPayloadLength(r io.Reader, marker byte) (int, error) {
	switch marker {
	case 126:
		var buf [2]byte
		if _, err := io.ReadFull(r, buf[:]); err != nil {
			return 0, err
		}
		return int(binary.BigEndian.Uint16(buf[:])), nil
	case 127:
		var buf [8]byte
		if _, err := io.ReadFull(r, buf[:]); err != nil {
			return 0, err
		}
		n := binary.BigEndian.Uint64(buf[:])
		if n > uint64(^uint(0)>>1) {
			return 0, fmt.Errorf("websocket payload too large")
		}
		return int(n), nil
	default:
		return int(marker), nil
	}
}

func applyMask(payload []byte, mask [4]byte) {
	for i := range payload {
		payload[i] ^= mask[i%4]
	}
}
