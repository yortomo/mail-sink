// Very simple server that mimics a smtp-server, it is very forgiving
// about what you send to and tends to agree (OK) most of the time.
package main

import (
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/textproto"
	"os"

	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

const (
	CODE_IN_DATA          = 0
	CODE_READY            = 220
	CODE_CLOSING          = 221
	CODE_ACCEPT           = 250
	CODE_START_MAIL_INPUT = 354
)

var (
	connectionCount = promauto.NewCounter(prometheus.CounterOpts{
		Name: "mail_sink_connections_total",
		Help: "The total number of connections accepted",
	})
	errorCount = promauto.NewCounter(prometheus.CounterOpts{
		Name: "mail_sink_errors_total",
		Help: "The total number of errors encountered",
	})
)

func listenTCP(host string, port int) (*net.TCPListener, error) {
	service := fmt.Sprintf("%s:%v", host, port)
	tcpAddr, err := net.ResolveTCPAddr("tcp", service)
	if err != nil {
		return nil, fmt.Errorf("could not resolve address: %w", err)
	}

	return net.ListenTCP("tcp", tcpAddr)
}

func respond(conn *textproto.Conn, code int, msg string) error {
	return conn.PrintfLine("%d %s", code, msg)
}

type SinkClient struct {
	conn     *textproto.Conn
	dataSent bool
}

func NewSinkClient(conn *textproto.Conn) *SinkClient {
	return &SinkClient{conn: conn, dataSent: false}
}

// handleQuery process client request and control it's simple state
func (s *SinkClient) handleQuery(line string) (code int, msg string) {
	switch {
	case s.dataSent && line == ".":
		s.dataSent = false
		code, msg = CODE_ACCEPT, "Ok: queued as 31337"
	case s.dataSent:
		code, msg = CODE_IN_DATA, ""
	case !s.dataSent && strings.ToLower(line) == "quit":
		code, msg = CODE_CLOSING, "Bye"
	case strings.HasPrefix(strings.ToLower(line), "data"):
		s.dataSent = true
		code, msg = CODE_START_MAIL_INPUT, "End data with <CR><LF>.<CR><LF>"
	default:
		code, msg = CODE_ACCEPT, "Ok"
	}

	return code, msg
}

type SinkServer struct {
	listener       *net.TCPListener
	heloHostName   string
	metricEnable   bool
	metricListener *net.TCPListener
}

func NewSinkServer(heloHostName, listenIntf string, listenPort int) (*SinkServer, error) {
	s := &SinkServer{metricEnable: false, heloHostName: heloHostName}
	var err error
	s.listener, err = listenTCP(listenIntf, listenPort)

	return s, err
}

// RunMetrics run metric service on dedicated go routine
func (s *SinkServer) RunMetrics(port int, host, path string) (err error) {
	s.metricEnable = true
	s.metricListener, err = listenTCP(host, port)
	if err != nil {
		return err
	}

	errCh := make(chan error, 1)
	mux := http.NewServeMux()
	mux.Handle(path, promhttp.Handler())
	go func() {
		if err := http.Serve(s.metricListener, mux); err != nil {
			slog.Error("Error starting metrics server", "error", err)
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		return err
	case <-time.After(500 * time.Millisecond):
		return nil
	}
}

func (s *SinkServer) ListenAndServe() error {
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			slog.Error("error accepting client", "error", err)
			if s.metricEnable {
				errorCount.Inc()
			}
			continue
		}
		if s.metricEnable {
			connectionCount.Inc()
		}
		go s.HandleClient(conn)
	}
}

// HandleClient processing client's smtp command per connection
func (s *SinkServer) HandleClient(conn net.Conn) {
	sc := NewSinkClient(textproto.NewConn(conn))
	remoteClient := conn.RemoteAddr().String()
	slog.Debug("Handling connection for: " + remoteClient)

	defer func() {
		slog.Debug("Closing connection", "client", remoteClient)
		sc.conn.Close()
	}()

	// Start off greeting our client
	greet := fmt.Sprintf("%s SMTP mail-sink", s.heloHostName)
	if err := respond(sc.conn, CODE_READY, greet); err != nil {
		slog.Error("error sending greeting", "client", remoteClient, "error", err)
		return
	}

	var code int
	var msg string
	for {
		line, err := sc.conn.ReadLine()
		if err != nil {
			if err != io.EOF {
				slog.Debug("error ReadLine", "client", remoteClient, "error", err)
				errorCount.Inc()
			}
			return
		}

		code, msg = sc.handleQuery(line)

		// reply if it's not in DATA
		if code != CODE_IN_DATA {
			if err := respond(sc.conn, code, msg); err != nil {
				slog.Error("error responding", "client", remoteClient, "error", err)
				return
			}
			if code == CODE_CLOSING {
				return
			}
		}
	}
}

func main() {
	// cli flags
	listenPort := flag.Int("p", 25, "listen port")
	listenIntf := flag.String("i", "localhost", "listen on interface")
	heloHostname := flag.String("H", "localhost", "hostname to greet with")
	debugEnable := flag.Bool("debug", false, "enable debug logging")
	metricsEnable := flag.Bool("metrics-enable", false, "enable metric and publish the endpoint")
	metricsPort := flag.Int("metrics-port", 9090, "port to serve prometheus metrics")
	metricsHost := flag.String("metrics-host", "localhost", "host to serve prometheus metrics")
	metricsPath := flag.String("metrics-path", "/metrics", "http path where prometheus will listen to")

	flag.Parse()

	logLevel := slog.LevelInfo
	if *debugEnable {
		logLevel = slog.LevelDebug
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: logLevel,
	}))
	slog.SetDefault(logger)

	slog.Info("Starting mail-sink", "interface", *listenIntf, "port", *listenPort)

	sink, err := NewSinkServer(*heloHostname, *listenIntf, *listenPort)
	if err != nil {
		slog.Error("Failed to create server", "error", err)
		os.Exit(1)
	}

	if *metricsEnable {
		slog.Info("Serving metrics", "port", *metricsPort, "path", *metricsPath, "host", *metricsHost)
		if err := sink.RunMetrics(*metricsPort, *metricsHost, *metricsPath); err != nil {
			slog.Error("Error starting metrics server", "error", err)
			os.Exit(1)
		}
	}

	sink.ListenAndServe()
}
