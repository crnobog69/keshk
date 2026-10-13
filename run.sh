#!/usr/bin/env bash
# =====================================================================
# Keshk — скрипта за компајлирање и покретање на Linux / Raspberry Pi 5
# =====================================================================
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR"

echo "[INFO] Keshk — покретање..."

# 1. Провера Go инсталације
if ! command -v go >/dev/null 2>&1; then
    echo "[ERROR] Go није инсталиран!"
    echo "Инсталирај Go (нпр. на Raspberry Pi OS):"
    echo "  sudo apt update && sudo apt install -y golang"
    echo "  или скини званични arm64 тарбал са https://go.dev/dl/"
    exit 1
fi

# 2. Креирање .env фајла ако не постоји
if [ ! -f ".env" ]; then
    echo "[INFO] Креирам .env из .env.example..."
    if [ -f ".env.example" ]; then
        cp .env.example .env
        SECRET=$(head -c 32 /dev/urandom | xxd -p 2>/dev/null || od -A n -N 32 -t x1 /dev/urandom | tr -d ' \n' 2>/dev/null || echo "keshk_default_secret_$(date +%s)")
        sed -i "s/SESSION_SECRET=/SESSION_SECRET=${SECRET}/" .env
        sed -i "s/KESHK_PASSWORD=промени-ме/KESHK_PASSWORD=keshk/" .env
        echo "[OK] Креиран .env са насумичним SESSION_SECRET (лозинка: keshk)"
    else
        echo "[ERROR] Недостаје .env.example фајл!"
        exit 1
    fi
fi

# 3. Креирање директоријума за базу ако не постоји
mkdir -p data

# 4. Компајлирање
echo "[BUILD] Компајлирам Keshk..."
CGO_ENABLED=0 go build -ldflags="-s -w" -o keshk .
chmod +x keshk

# 5. Покретање
echo "[START] Покрећем Keshk сервер..."
echo "[INFO] Отвори у прегледачу: http://localhost:3000 (или http://<IP_adresa_malinice>:3000)"
pkill -x keshk 2>/dev/null || true
exec ./keshk
