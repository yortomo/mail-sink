package main

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/textproto"
	"strings"
	"testing"
	"time"
)

func TestHandleQuery(t *testing.T) {
	testCases := map[string]struct {
		name          string
		clientState   *SinkClient
		input         string
		expectedCode  int
		expectedReply string
		expectedData  bool
	}{
		"normal command": {
			clientState: &SinkClient{
				dataSent: false,
			},
			input:         "helo localhost",
			expectedCode:  250,
			expectedReply: "Ok",
			expectedData:  false,
		},
		"start DATA": {
			clientState: &SinkClient{
				dataSent: false,
			},
			input:         "data",
			expectedCode:  354,
			expectedReply: "End data with <CR><LF>.<CR><LF>",
			expectedData:  true,
		},
		"in DATA state": {
			clientState: &SinkClient{
				dataSent: true,
			},
			input:         "some email content",
			expectedCode:  0,
			expectedReply: "",
			expectedData:  true,
		},
		"end DATA": {
			clientState: &SinkClient{
				dataSent: true,
			},
			input:         ".",
			expectedCode:  250,
			expectedReply: "Ok: queued as 31337",
			expectedData:  false,
		},
	}

	for name, tc := range testCases {
		t.Run(name, func(t *testing.T) {
			code, reply := tc.clientState.handleQuery(tc.input)

			if code != tc.expectedCode {
				t.Errorf("expected code %d, got %d", tc.expectedCode, code)
			}
			if reply != tc.expectedReply {
				t.Errorf("expected reply %q, got %q", tc.expectedReply, reply)
			}
			if tc.clientState.dataSent != tc.expectedData {
				t.Errorf("expected dataSent state %v, got %v", tc.expectedData, tc.clientState.dataSent)
			}
		})
	}
}

func TestIntegration(t *testing.T) {
	listenIntf := "127.0.0.1"
	listenPort := 0 // random port by OS

	server, err := NewSinkServer("localhost", listenIntf, listenPort)
	if err != nil {
		t.Fatalf("Failed to create server: %v", err)
	}

	go func() {
		server.ListenAndServe()
	}()

	// Give it a moment to start the server
	time.Sleep(100 * time.Millisecond)
	addr := server.listener.Addr().String()

	// simulate smtp client connection
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}
	defer conn.Close()

	reader := bufio.NewReader(conn)
	tp := textproto.NewReader(reader)

	// Check greeting
	line, err := tp.ReadLine()
	if err != nil {
		t.Fatalf("Failed to read greeting: %v", err)
	}
	if !strings.Contains(line, "220") {
		t.Errorf("Expected greeting 220, got: %s", line)
	}

	// Send EHLO
	fmt.Fprintf(conn, "EHLO localhost\r\n")
	line, err = tp.ReadLine()
	if err != nil {
		t.Fatalf("Failed to read EHLO response: %v", err)
	}
	if !strings.Contains(line, "250") {
		t.Errorf("Expected 250, got: %s", line)
	}

	// Send DATA
	fmt.Fprintf(conn, "DATA\r\n")
	line, err = tp.ReadLine()
	if err != nil {
		t.Fatalf("Failed to read DATA response: %v", err)
	}
	if !strings.Contains(line, "354") {
		t.Errorf("Expected 354, got: %s", line)
	}

	// Send Content
	fmt.Fprintf(conn, "Subject: test\r\n\r\nBody\r\n.\r\n")
	line, err = tp.ReadLine()
	if err != nil {
		t.Fatalf("Failed to read End Data response: %v", err)
	}
	if !strings.Contains(line, "250") {
		t.Errorf("Expected 250, got: %s", line)
	}

	// Send QUIT
	fmt.Fprintf(conn, "QUIT\r\n")
	line, err = tp.ReadLine()
	if err != nil {
		t.Fatalf("Failed to read QUIT response: %v", err)
	}
	if !strings.Contains(line, "221") {
		t.Errorf("Expected 221, got: %s", line)
	}
}

func TestMetrics(t *testing.T) {
	// Start Sink Server
	server, err := NewSinkServer("localhost", "127.0.0.1", 0)
	if err != nil {
		t.Fatalf("Failed to create sink server: %v", err)
	}
	server.RunMetrics(0, "127.0.0.1", "/metrics")
	go func() {
		server.ListenAndServe()
	}()

	// allow a time to start
	time.Sleep(500 * time.Millisecond)
	sinkAddr := server.listener.Addr().String()
	metricAddr := server.metricListener.Addr().String()

	// Connect to trigger connection count
	conn, err := net.Dial("tcp", sinkAddr)
	if err != nil {
		t.Fatalf("Failed to connect to sink server: %v", err)
	}
	conn.Close()

	// Allow metric update
	time.Sleep(100 * time.Millisecond)

	// Fetch metrics
	resp, err := http.Get("http://" + metricAddr + "/metrics")
	if err != nil {
		t.Fatalf("Failed to fetch metrics: %v", err)
	}
	defer resp.Body.Close()

	if resp.Status != "200 OK" {
		t.Errorf("expecting return 200 return status, got %s", resp.Status)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("Failed to read metrics body: %v", err)
	}

	// Check for specific metric
	metricsOutput := string(body)
	if !strings.Contains(metricsOutput, "mail_sink_connections_total") {
		t.Errorf("Metrics output does not contain mail_sink_connections_total")
	}

	// Since we made 1 connection, we expect the counter to be at least 1 (could be more if other tests ran)
	// Parsing strictly:
	found := false
	lines := strings.Split(metricsOutput, "\n")
	for _, line := range lines {
		if strings.HasPrefix(line, "mail_sink_connections_total") && !strings.HasPrefix(line, "#") {
			// Line format: mail_sink_connections_total 1
			parts := strings.Fields(line)
			if len(parts) == 2 {
				if parts[1] != "0" {
					found = true
					break
				}
			}
		}
	}

	if !found {
		t.Errorf("Expected mail_sink_connections_total to be > 0, got output:\n%s", metricsOutput)
	}
}
