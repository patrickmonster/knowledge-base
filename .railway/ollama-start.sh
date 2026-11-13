#!/bin/sh
set -eu

# Start with OLLAMA_HOST=[::]:11434 for Railway's private network.
ollama serve &
server_pid=$!
cleanup() {
  kill "$server_pid" 2>/dev/null || true
  wait "$server_pid" 2>/dev/null || true
}
trap cleanup EXIT
trap 'exit 0' TERM INT

# CLI calls stay on loopback; only the background server keeps the bind address.
export OLLAMA_HOST=http://127.0.0.1:11434
attempts=0
until ollama list >/dev/null 2>&1; do
  kill -0 "$server_pid"
  attempts=$((attempts + 1))
  if [ "$attempts" -ge 120 ]; then
    echo "Ollama failed to become ready" >&2
    exit 1
  fi
  sleep 1
done

# The volume persists the model so subsequent deployments do not download it.
ollama show bge-m3 >/dev/null 2>&1 || ollama pull bge-m3
echo "knowledge-base embedding model bge-m3 is ready"
wait "$server_pid"
