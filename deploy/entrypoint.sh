#!/bin/sh
set -e

case "${1:-all}" in
  api)
    exec /app/bin/agent-mem-api
    ;;
  mcp)
    exec /app/bin/agent-mem-mcp
    ;;
  py)
    exec uvicorn medha.api:app --host 0.0.0.0 --port "${PY_PORT:-5000}"
    ;;
  all)
    exec supervisord -n -c /etc/supervisor/conf.d/medha.conf
    ;;
  *)
    exec "$@"
    ;;
esac
