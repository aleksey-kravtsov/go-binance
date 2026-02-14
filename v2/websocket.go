package binance

import (
	"context"
	"net/http"
	"net/url"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

// WsHandler handle raw websocket message
type WsHandler func(message []byte)

// ErrHandler handles errors
type ErrHandler func(err error)

// WsConfig webservice configuration
type WsConfig struct {
	Endpoint string
	Header   http.Header
	Proxy    *string
}

func newWsConfig(endpoint string) *WsConfig {
	return &WsConfig{
		Endpoint: endpoint,
		Proxy:    getWsProxyUrl(),
		Header:   make(http.Header),
	}
}

func wsServe(cfg *WsConfig, handler WsHandler, errHandler ErrHandler) (doneC, stopC chan struct{}, err error) {
	return wsServeWithConnHandler(cfg, handler, errHandler)
}

type ConnHandler func(context.Context, *websocket.Conn)

// WsServeWithConnHandler serves websocket with custom connection handler, useful for custom keepalive
var wsServeWithConnHandler = func(cfg *WsConfig, handler WsHandler, errHandler ErrHandler) (doneC, stopC chan struct{}, err error) {
	proxy := http.ProxyFromEnvironment
	if cfg.Proxy != nil {
		u, err := url.Parse(*cfg.Proxy)
		if err != nil {
			return nil, nil, err
		}
		proxy = http.ProxyURL(u)
	}
	Dialer := websocket.Dialer{
		Proxy:             proxy,
		HandshakeTimeout:  45 * time.Second,
		EnableCompression: true,
	}

	c, _, err := Dialer.Dial(cfg.Endpoint, cfg.Header)
	if err != nil {
		return nil, nil, err
	}
	c.SetReadLimit(655350)
	doneC = make(chan struct{})
	stopC = make(chan struct{})

	go func() {
		defer close(doneC)
		defer c.Close()

		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()

		silent := false

		for {
			select {
			case <-ticker.C:
				if err := c.WriteControl(
					websocket.PingMessage,
					nil,
					time.Now().Add(time.Second),
				); err != nil {
					if !silent {
						errHandler(err)
					}
					return
				}

			case <-stopC:
				silent = true
				return

			default:
				_, message, err := c.ReadMessage()
				if err != nil {
					if !silent {
						errHandler(err)
					}
					return
				}
				handler(message)
			}
		}
	}()

	return
}

// keepAliveWithPing Keepalive by actively sending ping messages
func keepAliveWithPing(interval time.Duration, pongTimeout time.Duration) ConnHandler {
	return func(ctx context.Context, c *websocket.Conn) {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		var lastResponse int64
		atomic.StoreInt64(&lastResponse, time.Now().Unix())
		c.SetPongHandler(func(appData string) error {
			atomic.StoreInt64(&lastResponse, time.Now().Unix())
			return nil
		})

		lastPongTicker := time.NewTicker(pongTimeout)
		defer lastPongTicker.Stop()

		go func() {
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					if err := c.WriteControl(websocket.PingMessage, []byte{}, time.Now().Add(WebsocketPingTimeout)); err != nil {
						return
					}
				case <-lastPongTicker.C:
					if time.Since(time.Unix(atomic.LoadInt64(&lastResponse), 0)) > pongTimeout {
						c.Close()
						return
					}
				}
			}

		}()
	}
}

// keepAliveWithPong Keepalive by responding to ping messages
func keepAliveWithPong(ctx context.Context, c *websocket.Conn, timeout time.Duration) {
	ticker := time.NewTicker(timeout)
	defer ticker.Stop()

	var lastResponse int64
	atomic.StoreInt64(&lastResponse, time.Now().Unix())

	c.SetPingHandler(func(pingData string) error {
		// Respond with Pong using the server's PING payload
		err := c.WriteControl(
			websocket.PongMessage,
			[]byte(pingData),
			time.Now().Add(WebsocketPongTimeout), // Short deadline to ensure timely response
		)
		if err != nil {
			return err
		}

		atomic.StoreInt64(&lastResponse, time.Now().Unix())

		return nil
	})

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if time.Since(time.Unix(atomic.LoadInt64(&lastResponse), 0)) > timeout {
					c.Close()
					return
				}
			}
		}
	}()
}

var WsGetReadWriteConnection = func(cfg *WsConfig) (*websocket.Conn, error) {
	proxy := http.ProxyFromEnvironment
	if cfg.Proxy != nil {
		u, err := url.Parse(*cfg.Proxy)
		if err != nil {
			return nil, err
		}
		proxy = http.ProxyURL(u)
	}

	Dialer := websocket.Dialer{
		Proxy:             proxy,
		HandshakeTimeout:  45 * time.Second,
		EnableCompression: false,
	}

	c, _, err := Dialer.Dial(cfg.Endpoint, cfg.Header)
	if err != nil {
		return nil, err
	}

	return c, nil
}
