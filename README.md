# freeTG

Локальный SOCKS5-прокси на Go для Telegram. Соединения к Telegram Data Center заворачиваются в WebSocket через официальные `*.web.telegram.org`, а остальной TCP-трафик проходит напрямую.

Проект рассчитан на:

- Linux
- Android как нативный бинарник `GOOS=android`

## Как это работает

Схема такая:

```text
Telegram
  -> SOCKS5 127.0.0.1:PORT
  -> freeTG
     -> Telegram IP/DC -> WSS to pluto|venus|aurora|vesta|flora.web.telegram.org/apiws
     -> other traffic  -> direct TCP
```

DC-маппинг:

- DC1 -> `wss://pluto.web.telegram.org/apiws`
- DC2 -> `wss://venus.web.telegram.org/apiws`
- DC3 -> `wss://aurora.web.telegram.org/apiws`
- DC4 -> `wss://vesta.web.telegram.org/apiws`
- DC5 -> `wss://flora.web.telegram.org/apiws`

## Возможности

- локальный SOCKS5 без авторизации
- определение Telegram DC по IP и по первым 64 байтам obfuscated2 init
- WebSocket relay для Telegram
- direct TCP passthrough для остального трафика
- сборка без `cgo`
- поддержка `HTTPS_PROXY` и `ALL_PROXY` для исходящих WSS-подключений

## Локальный запуск

```bash
go run . -listen 127.0.0.1:1080
```

Или собранный бинарник:

```bash
./build/freeTG-linux-amd64 -listen 127.0.0.1:1080
```

Настройки SOCKS5 в Telegram:

- сервер: `127.0.0.1`
- порт: `1080`
- логин: пусто
- пароль: пусто

Если у системы доступ к `*.web.telegram.org` есть только через локальный proxy, выставьте переменную окружения:

```bash
export https_proxy=http://127.0.0.1:7897
```

## Сборка

Linux:

```bash
mkdir -p build
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o build/freeTG-linux-amd64 .
```

Android:

```bash
mkdir -p build
GOOS=android GOARCH=arm64 CGO_ENABLED=0 go build -o build/freeTG-android-arm64 .
```

## Android

Это не APK, а обычный ELF-бинарник.

Через `adb`:

```bash
adb push build/freeTG-android-arm64 /data/local/tmp/freeTG
adb shell chmod +x /data/local/tmp/freeTG
adb shell /data/local/tmp/freeTG -listen 127.0.0.1:8989
```

Через Termux:

```bash
mkdir -p ~/bin
cp build/freeTG-android-arm64 ~/bin/freeTG
chmod +x ~/bin/freeTG
~/bin/freeTG -listen 127.0.0.1:8989
```

После этого в Telegram на Android укажите:

- сервер: `127.0.0.1`
- порт: `8989`

## systemd

В репозитории есть:

- [freeTG.service](/home/falearn/petProjects/tglock/freeTG.service)
- [freeTG.env](/home/falearn/petProjects/tglock/freeTG.env)

Установка системного сервиса:

```bash
sudo install -m 755 /home/falearn/petProjects/tglock/build/freeTG-linux-amd64 /usr/local/bin/freeTG
sudo install -m 644 /home/falearn/petProjects/tglock/freeTG.service /etc/systemd/system/freeTG.service
sudo install -m 644 /home/falearn/petProjects/tglock/freeTG.env /etc/default/freeTG
sudo systemctl daemon-reload
sudo systemctl enable --now freeTG.service
```

Проверка:

```bash
sudo systemctl status --no-pager freeTG.service
```

Сервис слушает:

```text
127.0.0.1:8989
```

## Структура проекта

```text
main.go
go.mod
internal/proxy/server.go
internal/proxy/socks5.go
internal/proxy/telegram.go
internal/proxy/websocket.go
internal/proxy/relay.go
freeTG.service
freeTG.env
```

## Ограничения

- это CLI, без GUI
- Android-сборка здесь это бинарник, не APK
- если прямой доступ к `*.web.telegram.org:443` заблокирован, нужен рабочий `HTTPS_PROXY` или `ALL_PROXY`
