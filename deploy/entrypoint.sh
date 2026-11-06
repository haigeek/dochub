#!/bin/sh
set -eu

mkdir -p /data

if [ -n "${SSH_KEY_SOURCE:-}" ] && [ -f "${SSH_KEY_SOURCE}" ]; then
  cp "${SSH_KEY_SOURCE}" /run/dochub-ssh/id_ed25519
  SSH_KEY_PATH=/run/dochub-ssh/id_ed25519
  export SSH_KEY_PATH
fi

if [ -n "${KNOWN_HOSTS_SOURCE:-}" ] && [ -f "${KNOWN_HOSTS_SOURCE}" ]; then
  cp "${KNOWN_HOSTS_SOURCE}" /run/dochub-ssh/known_hosts
  SSH_KNOWN_HOSTS_PATH=/run/dochub-ssh/known_hosts
  export SSH_KNOWN_HOSTS_PATH
fi

if [ -f /run/dochub-ssh/id_ed25519 ]; then
  chmod 0400 /run/dochub-ssh/id_ed25519
fi
if [ -f /run/dochub-ssh/known_hosts ]; then
  chmod 0444 /run/dochub-ssh/known_hosts
fi

chown -R dochub:dochub /data /run/dochub-ssh
exec su-exec dochub "$@"
