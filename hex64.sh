#!/usr/bin/env bash
# Generiše 64 nasumična hex karaktera (32 bajta) iz kriptografski sigurnog izvora.
set -euo pipefail

if command -v openssl >/dev/null 2>&1; then
    openssl rand -hex 32
else
    head -c 32 /dev/urandom | od -An -tx1 | tr -d ' \n'
    echo
fi