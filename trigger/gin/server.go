package gin

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/project-flogo/core/support/log"
)

const (
	httpDefaultAddr    = ":8080"
	httpDefaultTlsAddr = ":8443"

	httpDefaultReadTimeout  = 60 * time.Second
	httpDefaultWriteTimeout = 60 * time.Second
)

type Option func(*Server)

// TLS option enables TLS on the server
func TLS(certFile, keyFile string) Option {
	return func(s *Server) {
		s.tlsEnabled = true
		s.certFile = certFile
		s.keyFile = keyFile

		if s.srv.Addr == "" {
			s.srv.Addr = httpDefaultTlsAddr
		}
	}
}

// Timeouts options lets you set the read and write timeouts of the server
func Timeouts(readTimeout, writeTimeout time.Duration) Option {
	return func(s *Server) {
		s.srv.ReadTimeout = readTimeout
		s.srv.WriteTimeout = writeTimeout
	}
}

type Server struct {
	running bool
	srv     *http.Server

	tlsEnabled bool
	certFile   string
	keyFile    string
}

func NewServer(addr string, handler http.Handler, opts ...Option) (*Server, error) {
	if addr == "" {
		addr = httpDefaultAddr
	}

	srv := &Server{}
	srv.srv = &http.Server{
		Addr:         addr,
		Handler:      handler,
		ReadTimeout:  httpDefaultReadTimeout,
		WriteTimeout: httpDefaultWriteTimeout,
	}

	for _, opt := range opts {
		opt(srv)
	}

	if err := srv.validateInit(); err != nil {
		return nil, err
	}

	return srv, nil
}

func (s *Server) Start() error {

	if s.running {
		return nil
	}

	if err := s.validateStart(); err != nil {
		return err
	}

	fullAddr := s.srv.Addr
	if fullAddr[0] == ':' {
		fullAddr = "0.0.0.0" + s.srv.Addr
	}

	s.running = true

	if s.tlsEnabled {

		go func() {

			log.RootLogger().Infof("Rest Trigger listening on https://%s", fullAddr)

			if err := s.srv.ListenAndServeTLS(s.certFile, s.keyFile); err != nil {
				s.running = false
				if !errors.Is(err, http.ErrServerClosed) {
					log.RootLogger().Error(err)
				}
			}
		}()
	} else {
		go func() {

			log.RootLogger().Infof("Rest Trigger listening on http://%s", fullAddr)

			if err := s.srv.ListenAndServe(); err != nil {
				s.running = false
				if !errors.Is(err, http.ErrServerClosed) {
					log.RootLogger().Error(err)
				}
			}
		}()
	}

	return nil
}

// Stop stops the server
func (s *Server) Stop() error {

	if !s.running {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	return s.srv.Shutdown(ctx)
}

func (s *Server) validateStart() error {

	//check if port is available
	ln, err := net.Listen("tcp", s.srv.Addr)
	if err != nil {
		return err
	}
	ln.Close()

	return nil
}

func (s *Server) validateInit() error {

	if s.tlsEnabled {
		// using tls, so validate cert & key

		if s.certFile == "" || s.keyFile == "" {
			return fmt.Errorf("when TLS is enabled, both cert file and key file must be specified")
		}

		_, err := tls.LoadX509KeyPair(s.certFile, s.keyFile)
		if err != nil {
			return err
		}
	}

	return nil
}
