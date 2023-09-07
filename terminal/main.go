package main

import (
	"context"
	"fmt"
	"github.com/23233/ggg/logger"
	"github.com/23233/ggg/ut"
	"github.com/kataras/neffos"
	"github.com/kataras/neffos/gobwas"
	"github.com/txthinking/socks5"
	"io"
	"net/http"
	"net/url"
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

func main() {
	// 获取公网IP
	publicIP, err := getPublicIP()
	if err != nil {
		logger.J.ErrorE(err, "未获取到公网ip")
		panic(err)
	}
	remoteAddr := ut.GetEnv("REMOTE_ADDR", "127.0.0.1")
	username := ut.RandomStr(6)
	password := ut.RandomStr(6)
	logger.J.Infof("连接:%s 公网ip:%s ss_name:%s ss_password:%s", remoteAddr, publicIP, username, password)
	println(fmt.Sprintf("测试是否生效 curl -v -x socks5://%s:%s@%s:3232 http://api.ipify.org", username, password, publicIP))

	// 创建 Socks5 代理服务器
	socks5Server, err := socks5.NewClassicServer("0.0.0.0:3232", publicIP, username, password, 60, 60)
	if err != nil {
		logger.J.ErrorE(err, "代理服务器未能成功上线")
		panic(err)
	}
	go socks5Server.ListenAndServe(nil)

	// 构建WebSocket URL，包含公网IP、Socks5端口、用户名和密码
	wsURL := fmt.Sprintf("ws://%s:3434/endpoint?ip=%s&port=3232&user=%s&pass=%s", remoteAddr, url.QueryEscape(publicIP), username, password)

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

	// 保持连接
	for {
		time.Sleep(30 * time.Second)
		logger.J.Infof("发起保活")
		c.Emit("keeplive", []byte("ping"))
	}
}
