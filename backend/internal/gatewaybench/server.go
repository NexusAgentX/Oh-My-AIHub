package gatewaybench

import (
	"crypto/tls"
	"io"
	"log"
	"net"
	"net/http"
	"sync/atomic"
)

// Server serves a handler over TLS on a loopback port and counts the TCP
// connections it accepts, which is how a run shows whether the gateway reuses
// its connections to an upstream.
type Server struct {
	listener *countingListener
	server   *http.Server
}

type countingListener struct {
	net.Listener
	accepted atomic.Int64
}

func (l *countingListener) Accept() (net.Conn, error) {
	conn, err := l.Listener.Accept()
	if err == nil {
		l.accepted.Add(1)
	}
	return conn, err
}

// Serve starts a TLS server for handler on 127.0.0.1 and a free port.
func Serve(handler http.Handler, config *tls.Config) (*Server, error) {
	raw, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	listener := &countingListener{Listener: raw}
	// The gateway drops connections mid-handshake when it cancels an attempt;
	// the server's log would only say so.
	server := &http.Server{Handler: handler, TLSConfig: config, ErrorLog: log.New(io.Discard, "", 0)}
	go func() { _ = server.ServeTLS(listener, "", "") }()
	return &Server{listener: listener, server: server}, nil
}

// Address is the host:port the server listens on.
func (s *Server) Address() string { return s.listener.Addr().String() }

// Port is the TCP port the server listens on.
func (s *Server) Port() string {
	_, port, _ := net.SplitHostPort(s.Address())
	return port
}

// Accepted is how many connections the server has accepted so far.
func (s *Server) Accepted() int64 { return s.listener.accepted.Load() }

// Close stops the server and drops its connections.
func (s *Server) Close() error { return s.server.Close() }
