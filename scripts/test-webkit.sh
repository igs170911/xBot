#!/usr/bin/env bash
# 用真的 Safari 核心（WebKit、iPhone 14 設定）跑手機畫面的 e2e（e2e/phone.spec.ts）。
# Playwright 的 WebKit 只支援 Ubuntu／Debian，所以在 Playwright 官方容器裡跑；要先 make build。
set -euo pipefail
cd "$(dirname "$0")/.."
ver=$(cd web && node -p "require('@playwright/test/package.json').version")
port=${XBOT_E2E_PORT:-8866}
docker run --rm --network host --ipc host -u "$(id -u):$(id -g)" -e HOME=/tmp \
  -e XBOT_E2E_WEBKIT=1 -e XBOT_E2E_PORT="$port" ${XBOT_AUDIT_SHOTS:+-e XBOT_AUDIT_SHOTS=1} \
  -v "$PWD:/work" -w /work/web "mcr.microsoft.com/playwright:v${ver}-noble" \
  npx playwright test e2e/phone.spec.ts --project=iphone-webkit "$@"
