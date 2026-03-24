package proxy

import (
	"crypto/aes"
	"crypto/cipher"
	"fmt"
	"io"
	"net"
)

func isTelegramTarget(host string) bool {
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	return telegramDC(ip) != 0
}

func telegramDC(ip net.IP) byte {
	v4 := ip.To4()
	if v4 == nil {
		return 0
	}

	switch {
	case v4[0] == 149 && v4[1] == 154:
		switch v4[2] {
		case 160, 161, 162, 163:
			return 1
		case 164, 165, 166, 167:
			return 2
		case 168, 169, 170, 171:
			return 3
		case 172, 173, 174, 175:
			return 1
		default:
			return 2
		}
	case v4[0] == 91 && v4[1] == 108:
		switch v4[2] {
		case 56, 57, 58, 59:
			return 5
		case 8, 9, 10, 11:
			return 3
		case 12, 13, 14, 15:
			return 4
		default:
			return 2
		}
	case v4[0] == 91 && v4[1] == 105:
		return 2
	case v4[0] == 185 && v4[1] == 76:
		return 2
	default:
		return 0
	}
}

func extractDCFromInit(init []byte) byte {
	if len(init) != 64 {
		return 0
	}

	block, err := aes.NewCipher(init[8:40])
	if err != nil {
		return 0
	}

	dec := make([]byte, len(init))
	copy(dec, init)
	stream := cipher.NewCTR(block, init[40:56])
	stream.XORKeyStream(dec, dec)

	dcID := int32(dec[60]) | int32(dec[61])<<8 | int32(dec[62])<<16 | int32(dec[63])<<24
	if dcID < 0 {
		dcID = -dcID
	}
	if dcID >= 1 && dcID <= 5 {
		return byte(dcID)
	}
	return 0
}

func relayViaTelegramWS(client net.Conn, req connectRequest) error {
	initBuf := make([]byte, 64)
	if _, err := io.ReadFull(client, initBuf); err != nil {
		return fmt.Errorf("read obfuscated init: %w", err)
	}

	dc := extractDCFromInit(initBuf)
	if dc == 0 {
		dc = telegramDC(net.ParseIP(req.Host))
	}
	if dc == 0 {
		dc = 2
	}

	ws, err := dialTelegramWS(dc)
	if err != nil {
		return err
	}
	defer ws.Close()

	if err := ws.WriteBinary(initBuf); err != nil {
		return fmt.Errorf("send init to websocket: %w", err)
	}

	return relayTCPAndWS(client, ws)
}
