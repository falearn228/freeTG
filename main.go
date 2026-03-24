package main

import (
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/falearn228/freeTG/internal/proxy"
)

func main() {
	listen := flag.String("listen", "127.0.0.1:1080", "SOCKS5 listen address")
	flag.Parse()

	logger := log.New(os.Stdout, "", log.LstdFlags)
	server := proxy.NewServer(*listen, logger)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		logger.Printf("shutting down proxy")
		server.Close()
	}()

	logger.Printf("starting SOCKS5 proxy on %s", *listen)
	if err := server.Run(); err != nil {
		logger.Fatalf("proxy stopped: %v", err)
	}
}
