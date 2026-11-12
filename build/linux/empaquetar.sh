#!/bin/sh
# Arma build/bin/osat-linux.tar.gz para repartir a otros usuarios de Linux: el programa, el ícono,
# el acceso del menú y el instalador (instalar.sh). Se corre en Linux desde cualquier carpeta del
# repo, con Go, Node y las dependencias de compilación de Linux (ver docs/desarrollo.md).
#
# Compila con go build (sin la CLI de Wails): los tags desktop y production son los que pone
# `wails build`, y webkit2_41 elige WebKitGTK 4.1.
set -e
cd "$(dirname "$0")/../.."

(cd frontend && npm ci && npm run build)
go build -tags desktop,production,webkit2_41 -trimpath -ldflags "-s -w" -o build/bin/osat .

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
mkdir "$tmp/osat-linux"
cp build/bin/osat "$tmp/osat-linux/osat"
cp build/linux/osat.desktop build/linux/instalar.sh "$tmp/osat-linux/"
cp build/appicon.png "$tmp/osat-linux/osat.png"
chmod 755 "$tmp/osat-linux/osat" "$tmp/osat-linux/instalar.sh"
tar czf build/bin/osat-linux.tar.gz -C "$tmp" osat-linux

echo "Listo: build/bin/osat-linux.tar.gz"
