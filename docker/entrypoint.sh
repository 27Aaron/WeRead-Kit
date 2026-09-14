#!/bin/sh
set -eu

db_path=${WEREAD_DB:-/data/weread.db}
db_dir=$(dirname "$db_path")

# 绑定挂载会遮住镜像中的 /data 权限；宿主机新目录通常由 Docker 以 root 创建。
# 仅调整数据库目录和 SQLite 文件，不递归修改挂载目录里的其他内容。
if [ "$(id -u)" = 0 ]; then
    mkdir -p "$db_dir"
    chown weread:weread "$db_dir"
    for file in "$db_path" "$db_path-wal" "$db_path-shm" "$db_path-journal"; do
        if [ -e "$file" ]; then
            chown weread:weread "$file"
            chmod 600 "$file"
        fi
    done
    exec su-exec weread:weread "$0" "$@"
fi

mkdir -p "$db_dir"
if [ ! -w "$db_dir" ] || [ ! -x "$db_dir" ]; then
    echo "数据库目录不可写: $db_dir。指定容器 user 时，请确保该用户有目录写入和访问权限。" >&2
    exit 1
fi
umask 077
exec "$@"
