package proxy

import (
	"fmt"
	"io"
	"net"
	"sync"
)

func relayDirect(client net.Conn, target string) error {
	remote, err := net.Dial("tcp", target)
	if err != nil {
		return fmt.Errorf("dial %s: %w", target, err)
	}
	defer remote.Close()

	if tcp, ok := remote.(*net.TCPConn); ok {
		_ = tcp.SetNoDelay(true)
	}

	return relayBidirectional(client, remote)
}

func relayBidirectional(a net.Conn, b net.Conn) error {
	var wg sync.WaitGroup
	errCh := make(chan error, 2)

	copySide := func(dst net.Conn, src net.Conn) {
		defer wg.Done()
		_, err := io.Copy(dst, src)
		if tcp, ok := dst.(*net.TCPConn); ok {
			_ = tcp.CloseWrite()
		} else {
			_ = dst.Close()
		}
		errCh <- err
	}

	wg.Add(2)
	go copySide(a, b)
	go copySide(b, a)
	wg.Wait()
	close(errCh)

	for err := range errCh {
		if err != nil && err != io.EOF {
			return err
		}
	}
	return nil
}

func relayTCPAndWS(client net.Conn, ws *wsConn) error {
	errCh := make(chan error, 2)
	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		buf := make([]byte, 32*1024)
		for {
			n, err := client.Read(buf)
			if n > 0 {
				if writeErr := ws.WriteBinary(buf[:n]); writeErr != nil {
					errCh <- writeErr
					return
				}
			}
			if err != nil {
				if err == io.EOF {
					errCh <- nil
				} else {
					errCh <- err
				}
				return
			}
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			opcode, payload, err := ws.ReadFrame()
			if err != nil {
				if err == io.EOF {
					errCh <- nil
				} else {
					errCh <- err
				}
				return
			}

			switch opcode {
			case wsOpcodeBinary:
				if _, err := client.Write(payload); err != nil {
					errCh <- err
					return
				}
			case wsOpcodePing:
				if err := ws.WriteControl(wsOpcodePong, payload); err != nil {
					errCh <- err
					return
				}
			case wsOpcodeClose:
				errCh <- nil
				return
			}
		}
	}()

	err := <-errCh
	_ = ws.Close()
	_ = client.Close()
	wg.Wait()
	return err
}
