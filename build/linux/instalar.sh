#!/bin/sh
# Instala osat para este usuario: el programa, el ícono y el acceso en el menú de aplicaciones.
set -e
cd "$(dirname "$0")"

if ! ldconfig -p | grep -q 'libwebkit2gtk-4.1.so.0'; then
  echo "Falta WebKitGTK 4.1. Instalalo con:"
  echo "  sudo apt install libwebkit2gtk-4.1-0"
  exit 1
fi

install -Dm755 osat "$HOME/.local/bin/osat"
install -Dm644 osat.png "$HOME/.local/share/icons/hicolor/1024x1024/apps/osat.png"
mkdir -p "$HOME/.local/share/applications"
sed "s|^Exec=.*|Exec=$HOME/.local/bin/osat|" osat.desktop > "$HOME/.local/share/applications/osat.desktop"
update-desktop-database "$HOME/.local/share/applications" 2>/dev/null || true

echo "Listo: buscá \"OSAT\" en el menú de aplicaciones."
