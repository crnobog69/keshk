#!/usr/bin/env bash
# =====================================================================
# Ресетовање Keshk базе података (Linux / Raspberry Pi 5)
# =====================================================================
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR"

# 1. Заустави сервер ако ради
if pgrep -x keshk >/dev/null 2>&1; then
    echo "[STOP] Заустављам Keshk процес ради брисања базе..."
    pkill -x keshk 2>/dev/null || true
    sleep 0.5
fi

# 2. Обриши SQLite фајлове базе у data/
if compgen -G "data/keshk.db*" > /dev/null; then
    rm -f data/keshk.db*
    echo "[OK] Обрисани фајлови базе из data/."
else
    echo "[INFO] База је већ празна (нема фајлова у data/)."
fi

mkdir -p data

echo "[OK] База је успешно ресетована на почетно чисто стање!"
echo "[INFO] Покрени сервер са: ./run.sh"
