#!/bin/bash
# Поднимает Docker-демон в облачной сессии Claude Code: запущенные процессы
# не переживают кеш облачного окружения. Локально ничего не делает.
[ "$CLAUDE_CODE_REMOTE" = "true" ] || exit 0
docker info >/dev/null 2>&1 && exit 0
setsid nohup dockerd </dev/null >/tmp/dockerd.log 2>&1 &
for _ in $(seq 30); do
  docker info >/dev/null 2>&1 && exit 0
  sleep 1
done
echo "dockerd did not start within 30s, see /tmp/dockerd.log"
exit 0
