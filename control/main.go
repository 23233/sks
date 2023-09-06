package main

import (
	"encoding/binary"
	"errors"
	"fmt"
	"github.com/23233/ggg/logger"
	"github.com/23233/ggg/ut"
	"github.com/kataras/iris/v12"
	"github.com/kataras/iris/v12/websocket"
	"github.com/kataras/neffos"
	"github.com/kataras/neffos/gobwas"
	"golang.org/x/net/proxy"
	"io"
	"log"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"
)

func getPublicIP() (string, error) {
	resp, err := http.Get("http://api.ipify.org")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	ip, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	return string(ip), nil
}

type ClientInfo struct {
	Conn    *neffos.Conn
	IP      string
	Port    string
	User    string
	Pass    string
	Lock    bool
	LockExp time.Time
}

type ClientManager struct {
	clients  map[*neffos.Conn]*ClientInfo
	mu       sync.Mutex
	clientID int
}

func (cm *ClientManager) AddClient(conn *neffos.Conn, info *ClientInfo) {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	cm.clients[conn] = info
}
func (cm *ClientManager) DelClient(conn *neffos.Conn) {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	delete(cm.clients, conn)
}

func (cm *ClientManager) GetClient(conn *neffos.Conn) (*ClientInfo, bool) {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	for _, info := range cm.clients {
		if info.Conn.ID() == conn.ID() {
			return info, true
		}
	}
	return nil, false
}

func (cm *ClientManager) GetNextClient() *ClientInfo {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	keys := make([]*neffos.Conn, 0, len(cm.clients))
	for k := range cm.clients {
		keys = append(keys, k)
	}

	if len(keys) == 0 {
		return nil
	}

	client := cm.clients[keys[cm.clientID%len(keys)]]
	cm.clientID++
	return client
}
func (cm *ClientManager) GetNextClientWithLock() *ClientInfo {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	lockDurationStr := ut.GetEnv("LOCK_DURATION_MS", "1000")
	lockDurationMs, err := strconv.Atoi(lockDurationStr)
	if err != nil {
		lockDurationMs = 1000 // 默认值为1000毫秒（1秒）
	}

	for _, client := range cm.clients {
		if client.Lock && time.Now().Before(client.LockExp) {
			continue
		}
		client.Lock = true
		client.LockExp = time.Now().Add(time.Duration(lockDurationMs) * time.Millisecond)
		return client
	}
	return nil
}

func main() {
	publicIp, _ := getPublicIP()
	app := iris.Default()

	clientManager := &ClientManager{
		clients: make(map[*neffos.Conn]*ClientInfo),
	}
	// 创建 WebSocket 服务器
	server := neffos.New(gobwas.DefaultUpgrader,
		neffos.Namespaces{
			"default": neffos.Events{
				neffos.OnNamespaceConnect: func(conn *neffos.NSConn, message neffos.Message) error {
					ctx := websocket.GetContext(conn.Conn)
					ip := ctx.URLParam("ip")
					port := ctx.URLParam("port")
					user := ctx.URLParam("user")
					pass := ctx.URLParam("pass")
					if len(ip) < 1 || len(port) < 1 || len(user) < 1 || len(pass) < 1 {
						return errors.New("未传递连接信息")
					}

					clientInfo := &ClientInfo{
						Conn: conn.Conn,
						IP:   ip,
						Port: port,
						User: user,
						Pass: pass,
					}
					clientManager.AddClient(conn.Conn, clientInfo)
					logger.J.Infof("[%s] 连接上服务器", ip)
					return nil
				},
				"keeplive": func(conn *neffos.NSConn, message neffos.Message) error {
					client, has := clientManager.GetClient(conn.Conn)
					if !has {
						logger.J.Errorf("[%s] 未找到连接信息", conn.Conn.ID())
						return nil
					}

					logger.J.Infof("[%s] keeplive", client.IP)
					return nil
				},
			},
		},
	)

	server.OnConnect = func(c *neffos.Conn) error {
		return nil
	}
	server.OnDisconnect = func(c *neffos.Conn) {
		client, has := clientManager.GetClient(c)
		if has {
			logger.J.Warnf("[%s] 断开连接", client.IP)
		}
		clientManager.DelClient(c)
	}
	app.Get("/endpoint", websocket.Handler(server))
	app.Get("/client", func(ctx iris.Context) {
		client := clientManager.GetNextClientWithLock()
		if client == nil {
			ctx.StatusCode(400)
			return
		}
		_, _ = ctx.Text(fmt.Sprintf("socks5://%s:%s@%s:%s", client.User, client.Pass, client.IP, client.Port))
	})
	logger.J.Infof("公网ip:%s", publicIp)

	listener, err := net.Listen("tcp", ":3535")
	if err != nil {
		log.Fatal("Error starting server:", err)
	}
	defer listener.Close()

	var handleClient = func(clientConn net.Conn) {
		defer clientConn.Close()

		// 从SOCKS5代理列表中获取一个代理
		nextProxy := clientManager.GetNextClient()
		if nextProxy == nil {
			logger.J.Errorf("未获取到任何一个有效的代理")
			return
		}
		socks5Addr := net.JoinHostPort(nextProxy.IP, nextProxy.Port)

		// 设置SOCKS5代理
		auth := &proxy.Auth{
			User:     nextProxy.User,
			Password: nextProxy.Pass,
		}
		dialer, err := proxy.SOCKS5("tcp", socks5Addr, auth, proxy.Direct)
		if err != nil {
			logger.J.ErrorE(err, "Error creating dialer")
			return
		}

		// 从客户端读取SOCKS5握手信息以获取目标地址和端口
		buffer := make([]byte, 256)
		_, err = io.ReadFull(clientConn, buffer[:8])
		if err != nil {
			logger.J.ErrorE(err, "Error reading SOCKS5 request")
			return
		}

		targetAddr := net.IP(buffer[4:8]).String()
		targetPort := binary.BigEndian.Uint16(buffer[8:10])

		// 连接到目标地址和端口（这里仅作示例）
		targetConn, err := dialer.Dial("tcp", net.JoinHostPort(targetAddr, fmt.Sprintf("%d", targetPort)))
		if err != nil {
			logger.J.ErrorE(err, "Error connecting to targer")
			return
		}
		defer targetConn.Close()

		// 创建双向数据传输
		go func() {
			io.Copy(targetConn, clientConn)
		}()
		io.Copy(clientConn, targetConn)
	}

	go func() {
		for {
			clientConn, err := listener.Accept()
			if err != nil {
				logger.J.ErrorE(err, "Error accepting connection")
				continue
			}
			go handleClient(clientConn)
		}
	}()

	app.Logger().Fatal(app.Listen(":3434"))
}
