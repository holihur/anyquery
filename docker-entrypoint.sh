#!/bin/sh
set -e
if [ "${SEED_DEMO:-false}" = "true" ]; then
  echo "Seeding demo workspace…"
  /app/anyquery-seed || echo "Seed failed (continuing)"
fi
exec /app/anyquery
