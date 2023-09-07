package main

import (
	"context"
	"errors"
	"fmt"
	"github.com/23233/ggg/logger"
	"github.com/23233/ggg/ut"
	"github.com/armon/go-socks5"
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

func (cl *ClientInfo) CreateDialer() (proxy.Dialer, error) {
	auth := &proxy.Auth{
		User:     cl.User,
		Password: cl.Pass,
	}
	return proxy.SOCKS5("tcp", net.JoinHostPort(cl.IP, cl.Port), auth, proxy.Direct)
}

type ClientManager struct {
	clients  map[*neffos.Conn]*ClientInfo
	mu       sync.Mutex
	clientID int
}

func (cm *ClientManager) AddClient(conn *neffos.Conn, info *ClientInfo) {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	for k, cl := range cm.clients {
		if info.IP == cl.IP {
			delete(cm.clients, k)
		}
	}
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

type SimpleCredentialStore struct{}

func (s SimpleCredentialStore) Valid(user, password string) bool {
	return user == "aaa" && password == "123"
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
					logger.J.Infof("[%s] 连接上服务器 port:%s un:%s ps:%s", ip, port, user, pass)
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

	logger.J.Infof("开启socket5代理: curl -v -x socks5://aaa:123@%s:3535 http://api.ipify.org", publicIp)

	// 创建一个自定义Dial函数，用于连接到另一个SOCKS5代理
	dial := func(ctx context.Context, network, addr string) (net.Conn, error) {
		//从SOCKS5代理列表中获取一个代理
		nextProxy := new(ClientInfo)
		nextProxy = clientManager.GetNextClient()
		if nextProxy == nil {
			logger.J.Errorf("未获取到任何一个有效的代理")
			return nil, errors.New("未找到有效的代理")
		}

		logger.J.Infof("选中的代理是 %s:%s", nextProxy.IP, nextProxy.Port)

		// 创建到该SOCKS5代理的Dialer
		dialer, err := nextProxy.CreateDialer()
		if err != nil {
			logger.J.ErrorE(err, "创建代理的socket5连接失败")
			return nil, err
		}

		return dialer.Dial(network, addr)
	}

	// 创建一个SOCKS5服务器配置
	conf := &socks5.Config{
		Dial:        dial,
		Credentials: SimpleCredentialStore{},
	}

	// 创建一个SOCKS5代理服务器
	sks, err := socks5.New(conf)
	if err != nil {
		log.Fatalf("Failed to create SOCKS5 server: %v", err)
	}

	go func() {
		// 监听并提供SOCKS5代理服务
		if err := sks.ListenAndServe("tcp", "0.0.0.0:3535"); err != nil {
			log.Fatalf("Failed to start SOCKS5 server: %v", err)
		}
	}()

	app.Logger().Fatal(app.Listen(":3434"))
}
