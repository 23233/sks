### 这里放置的是终端的代码

需求为
* 开启socket5代理
  * 随机生成连接的账号和密码
  * websocket 连接到中控
  * 持续发送ping -> pong

需要设置环境变量 `REMOTE_ADDR` 为中控的地址 必须设置!