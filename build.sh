#!/bin/bash
set -e


echo "创建各个文件夹 存在则跳过"

# 创建save文件夹
if [ ! -d "save" ]; then
  mkdir "save"
fi

# 创建docker文件保存文件夹

if [ ! -d "docker_data" ]; then
  mkdir "docker_data"
fi


docker pull "23233/sk5_control"

# 赋予执行权限 否则无法执行
chmod 777 docker-compose.yml build.sh

# 运行docker
docker compose down && docker compose up -d

echo "执行完成"
exit