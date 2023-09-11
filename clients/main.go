package main

import (
	"context"
	"fmt"
	"github.com/23233/ggg/logger"
	"github.com/23233/ggg/ut"
	"github.com/kataras/neffos"
	"github.com/kataras/neffos/gobwas"
	"io"
	"net/http"
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

func connectWebsocket() {
	publicIP, err := getPublicIP()
	if err != nil {
		logger.J.ErrorE(err, "未获取到公网ip")
		panic(err)
	}
	remoteAddr := ut.GetEnv("REMOTE_ADDR", "127.0.0.1")
	localPort := "4666"
	logger.J.Infof("连接:%s 公网ip:%s", remoteAddr, publicIP)
	println(fmt.Sprintf("测试是否生效 curl -v http://%s:%s/search?keywords=海阔天空", publicIP, localPort))

	// 构建WebSocket URL，包含公网IP、Socks5端口、用户名和密码
	wsURL := fmt.Sprintf("ws://%s:3636/endpoint?ip=%s&port=%s", remoteAddr, publicIP, localPort)

	handler := neffos.WithTimeout{
		ReadTimeout:  0,
		WriteTimeout: 0,
		Namespaces: neffos.Namespaces{
			"default": neffos.Events{
				neffos.OnNamespaceConnect: func(conn *neffos.NSConn, message neffos.Message) error {
					return nil
				},
			},
		},
	}

	// 创建 WebSocket 客户端
	client, err := neffos.Dial(context.TODO(), gobwas.DefaultDialer, wsURL, handler)
	if err != nil {
		logger.J.ErrorE(err, "创建websocket连接失败")
		panic(err)
	}
	logger.J.Infof("已连接上ws %s", client.ID)

	namespace := "default"

	c, err := client.Connect(context.TODO(), namespace)
	if err != nil {
		logger.J.ErrorE(err, "连接到namespace %s 失败", namespace)
		panic(err)
	}
	logger.J.Infof("已连接上 %s", namespace)
	// 保持连接
	for {
		time.Sleep(30 * time.Second)
		logger.J.Infof("发起保活")
		success := c.Emit("keeplive", []byte("ping"))
		if !success {
			logger.J.Errorf("发起保活失败 重新连接")
			break
		}
	}
	connectWebsocket()
}

func main() {
	connectWebsocket()
}
