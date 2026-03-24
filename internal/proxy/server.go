package proxy

import (
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

type Server struct {
	listenAddr string
	logger     *log.Logger
	listener   net.Listener
	closed     atomic.Bool
	connID     atomic.Uint64
	closeOnce  sync.Once
}

func NewServer(listenAddr string, logger *log.Logger) *Server {
	return &Server{
		listenAddr: listenAddr,
		logger:     logger,
	}
}

func (s *Server) Run() error {
	ln, err := net.Listen("tcp", s.listenAddr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", s.listenAddr, err)
	}
	s.listener = ln
	defer ln.Close()

	for {
		conn, err := ln.Accept()
		if err != nil {
			if s.closed.Load() || errors.Is(err, net.ErrClosed) {
				return nil
			}
			if ne, ok := err.(net.Error); ok && ne.Temporary() {
				time.Sleep(100 * time.Millisecond)
				continue
			}
			return fmt.Errorf("accept: %w", err)
		}

		id := s.connID.Add(1)
		go s.handleConn(id, conn)
	}
}

func (s *Server) Close() {
	s.closeOnce.Do(func() {
		s.closed.Store(true)
		if s.listener != nil {
			_ = s.listener.Close()
		}
	})
}

func (s *Server) handleConn(id uint64, conn net.Conn) {
	defer conn.Close()

	if tcp, ok := conn.(*net.TCPConn); ok {
		_ = tcp.SetNoDelay(true)
	}

	req, err := handshakeSOCKS5(conn)
	if err != nil {
		if !errors.Is(err, io.EOF) {
			s.logger.Printf("conn=%d handshake failed: %v", id, err)
		}
		return
	}

	if isTelegramTarget(req.Host) {
		if err := relayViaTelegramWS(conn, req); err != nil {
			s.logger.Printf("conn=%d ws relay failed: %v", id, err)
		}
		return
	}

	if err := relayDirect(conn, req.Address()); err != nil {
		s.logger.Printf("conn=%d direct relay failed: %v", id, err)
	}
}
