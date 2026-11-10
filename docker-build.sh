#!/usr/bin/env bash
# =====================================================================
# Keshk — сачува измене (commit порука: ДД.ММ.ГГГГ. ЧЧ:ММ:СС),
# направи ознаку ДД.ММ.ГГГГ_ЧЧ-ММ-СС и пошаље грану + таг И на gitcrn И на github
# Употреба:
#   ./docker-build.sh                   # commit 'ДД.ММ.ГГГГ. ЧЧ:ММ:СС' + таг + push
#   ./docker-build.sh "Опис измене"     # прилагођена commit порука
#   ./docker-build.sh --local           # локални Docker билд
# =====================================================================
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR"

BRANCH="master"
NOW_EPOCH="$(date +%s)"
SR_DATETIME="$(TZ=Europe/Belgrade date -d "@${NOW_EPOCH}" +'%d.%m.%Y. %H:%M:%S' 2>/dev/null || TZ=Europe/Belgrade date +'%d.%m.%Y. %H:%M:%S')"
TAG="$(TZ=Europe/Belgrade date -d "@${NOW_EPOCH}" +'%d.%m.%Y_%H-%M-%S' 2>/dev/null || TZ=Europe/Belgrade date +'%d.%m.%Y_%H-%M-%S')"

if [[ "${1:-}" == "--local" ]]; then
    echo "[BUILD] Локална изградња Docker слике keshk:${TAG} ..."
    docker build -t "keshk:${TAG}" -t "keshk:latest" .
    echo "[OK] Изграђене локалне слике: keshk:${TAG} и keshk:latest"
    exit 0
fi

MSG="${1:-$SR_DATETIME}"

git add .
if git diff --cached --quiet; then
    echo "[GIT] Нема нових измена за commit — користим тренутни HEAD."
else
    git commit -m "$MSG"
    echo "[COMMIT] ${MSG}"
fi

git tag "$TAG"
echo "[TAG] Креирана ознака: ${TAG}"

CRN_REMOTE=""
if git remote | grep -qx "gitcrn"; then
    CRN_REMOTE="gitcrn"
else
    CRN_REMOTE="$(git remote -v | awk '/(gitcrn|crnbg\.org).* \(push\)/ {print $1; exit}')"
fi

GH_REMOTE=""
if git remote | grep -qx "github"; then
    GH_REMOTE="github"
else
    GH_REMOTE="$(git remote -v | awk '/github\.com.* \(push\)/ {print $1; exit}')"
fi

if [[ -n "$CRN_REMOTE" ]]; then
    echo "[PUSH] Шаљем '${BRANCH}' и '${TAG}' на gitcrn (${CRN_REMOTE})..."
    git push "$CRN_REMOTE" "$BRANCH" "$TAG"
else
    echo "[WARN] Није пронађен gitcrn remote — прескачем слање на gitcrn."
fi

if [[ -n "$GH_REMOTE" ]]; then
    echo "[PUSH] Шаљем '${BRANCH}' и '${TAG}' на GitHub (${GH_REMOTE})..."
    git push "$GH_REMOTE" "$BRANCH" "$TAG"
    echo "[OK] Покренут је GitHub Actions билд за ознаке :${TAG} и :latest"
    echo "[INFO] За праћење изградње уживо укуцај: gh run watch"
else
    echo "[ERROR] Није пронађен 'github' remote! Додај га једном са:" >&2
    echo "  gh repo create keshk --private --source=. --remote=github" >&2
    exit 1
fi
