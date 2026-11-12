#!/bin/sh
set -eu

# 复制进容器的文件默认归 root；只让 MySQL 用户读取本轮服务端私钥。
chown mysql:mysql /etc/mysql/testkit/server-key.pem
chmod 600 /etc/mysql/testkit/server-key.pem
exec /usr/local/bin/docker-entrypoint.sh "$@"
