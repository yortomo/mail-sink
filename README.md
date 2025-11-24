# mail-sink

A forked version of stone's [mail-sink](https://github.com/stone/mail-sink) with following (additional features):

- JSON structured output using [slog](https://go.dev/blog/slog)
- Disable verbosed logging by default, can be activate by set `-debug` arg
- Publish connection and error count as prometheus metric, disabled by default
- Adjustable prometheus metric endpoint for host, port and http path

mail-sink is a utility program that implements a "black hole" function. It listens on the named host (or address) and port. It accepts Simple Mail Transfer Protocol (SMTP) messages from the network and discards them.

Written in Go: http://www.golang.org/
Usage:

```text
    $ ./mail-sink -h
    Usage of mail-sink:

    -H string
            hostname to greet with (default "localhost")
    -debug
            enable debug logging
    -i string
            listen on interface (default "localhost")
    -metrics-enable
            enable metric and publish the endpoint
    -metrics-host string
            host to serve prometheus metrics (default "localhost")
    -metrics-path string
            http path where prometheus will listen to (default "/metrics")
    -metrics-port int
            port to serve prometheus metrics (default 9090)
    -p int
            listen port (default 25)
```

## Installation

### Get from source

```bash
go get github.com/yortomo/mail-sink
```

### Container

From Github package

```text
ghcr.io/yortomo/mail-sink:latest
```
