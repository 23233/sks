package main

import (
	"errors"
	"fmt"
	"github.com/23233/ggg/logger"
	"github.com/kataras/iris/v12"
	"github.com/kataras/iris/v12/websocket"
	"github.com/kataras/neffos"
	"github.com/kataras/neffos/gobwas"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"sync"
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
	Conn  *neffos.Conn
	IP    string
	Port  string
	Proxy *httputil.ReverseProxy
}

func (c *ClientInfo) GetHost() string {
	return fmt.Sprintf("http://%s:%s", c.IP, c.Port)
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
	targetURL, err := url.Parse(info.GetHost())
	if err != nil {
		panic("Error parsing target URL: " + err.Error())
	}
	info.Proxy = httputil.NewSingleHostReverseProxy(targetURL)
	info.Proxy.ErrorHandler = func(writer http.ResponseWriter, request *http.Request, err error) {
		// 自定义错误处理
		writer.WriteHeader(http.StatusBadGateway)
		writer.Write([]byte("连接源站错误: " + err.Error()))
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

func (cm *ClientManager) GetAllIp() []string {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	var ips = make([]string, 0, len(cm.clients))

	for _, client := range cm.clients {
		ips = append(ips, client.GetHost())
	}
	return ips
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

					if len(ip) < 1 || len(port) < 1 {
						return errors.New("未传递连接信息")
					}

					clientInfo := &ClientInfo{
						Conn: conn.Conn,
						IP:   ip,
						Port: port,
					}
					clientManager.AddClient(conn.Conn, clientInfo)
					logger.J.Infof("[%s] 连接上服务器 port:%s ", ip, port)
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
	app.Get("/clients", func(ctx iris.Context) {
		_ = ctx.JSON(iris.Map{
			"count": len(clientManager.clients),
			"ips":   clientManager.GetAllIp(),
		})
	})
	app.Any("/{path:path}", func(ctx iris.Context) {

		client := clientManager.GetNextClient()

		// 执行代理转发
		client.Proxy.ServeHTTP(ctx.ResponseWriter(), ctx.Request())

	})
	logger.J.Infof("公网ip:%s", publicIp)

	println(fmt.Sprintf("测试是否生效 curl -v http://%s:%s/search?keywords=海阔天空", publicIp, "3636"))

	app.Logger().Fatal(app.Listen(":3636"))
}
