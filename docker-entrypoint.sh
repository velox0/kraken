#!/bin/sh
set -eu

ADMIN_EMAIL="${ADMIN_EMAIL:-admin@example.com}"
ADMIN_PASSWORD="${ADMIN_PASSWORD:-admin}"
ADMIN_NAME="${ADMIN_NAME:-Admin}"
ADMIN_BOOTSTRAP="${ADMIN_BOOTSTRAP:-true}"

if [ "$ADMIN_BOOTSTRAP" = "true" ]; then
  echo "Bootstrapping admin user: $ADMIN_EMAIL"
  # Retry briefly so first startup is resilient if DB is still accepting connections.
  i=0
  until /app/useradmin create --email "$ADMIN_EMAIL" --password "$ADMIN_PASSWORD" --name "$ADMIN_NAME"; do
    i=$((i + 1))
    if [ "$i" -ge 20 ]; then
      echo "Failed to bootstrap admin user after 20 attempts"
      exit 1
    fi
    sleep 1
  done
fi

exec /app/kraken
