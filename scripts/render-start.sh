#!/bin/sh
set -e

export PORT="${PORT:-10000}"

if [ -n "$PORT" ] && [ "$HTTP_ADDR" = "0.0.0.0:8080" ]; then
  unset HTTP_ADDR
fi

cleanup() {
  trap '' TERM INT
  echo "render-start: stopping child processes..."
  if [ -n "$WORKER_PID" ] && kill -0 "$WORKER_PID" 2>/dev/null; then
    kill -TERM "$WORKER_PID" 2>/dev/null || true
  fi
  if [ -n "$API_PID" ] && kill -0 "$API_PID" 2>/dev/null; then
    kill -TERM "$API_PID" 2>/dev/null || true
  fi
  wait "$WORKER_PID" 2>/dev/null || true
  wait "$API_PID" 2>/dev/null || true
  exit 1
}

trap cleanup TERM INT

(
  /app/bin/worker
  EXIT_CODE=$?
  echo "render-start: worker exited with code $EXIT_CODE"
  kill -TERM $$ 2>/dev/null || true
) &
WORKER_PID=$!

/app/bin/api &
API_PID=$!

wait "$API_PID" 2>/dev/null || true
echo "render-start: api exited"
cleanup
