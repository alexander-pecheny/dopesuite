# Свой сервер xy

Системные требования: сервер с Ubuntu/Debian, 1 ГБ памяти, домен.

## 1. Скачайте бинарники

Релизы лежат на [GitHub](https://github.com/alexander-pecheny/dopesuite/releases).
Положите бинарники в `/opt/xy`:

```sh
VERSION=2026.08.03
ARCH=amd64
BASE=https://github.com/alexander-pecheny/dopesuite/releases/download/xy%2F$VERSION
curl -fLO $BASE/xy-$VERSION-linux-$ARCH.tar.gz
curl -fLO $BASE/SHA256SUMS
sha256sum --check --ignore-missing SHA256SUMS

tar xzf xy-$VERSION-linux-$ARCH.tar.gz
sudo mkdir -p /opt/xy
sudo cp xy-$VERSION-linux-$ARCH/{xy-server,telegram-bot} /opt/xy/
```

## 2. Создайте пользователя, из-под которого запускается сервис, и папку для него

```sh
sudo useradd --system --home /var/lib/xy --shell /usr/sbin/nologin xy
sudo mkdir -p /var/lib/xy
sudo chown xy:xy /var/lib/xy
```

## 3. Настройки

`/etc/xy.env`, права `600` и владелец `root` — там лежит секрет бота.

```sh
PORT=9673
XY_ENV=production
XY_PUBLIC_URL=https://xy.example.org
XY_DB=/var/lib/xy/xy.db
XY_BLOBS=/var/lib/xy/blobs
XY_WASM_CACHE=/var/lib/xy/typst-wasm
XY_ADMIN_USER=ваш_логин
XY_BOT_TOKEN=токен-от-BotFather  # только если нужен вход через телеграм, см. п. 7
XY_BOT_NAME=имя_вашего_бота
```

## 4. Создайте сервис systemd

`/etc/systemd/system/xy.service`:

```ini
[Unit]
Description=xy
After=network.target

[Service]
Type=simple
User=xy
Group=xy
WorkingDirectory=/var/lib/xy
EnvironmentFile=/etc/xy.env
ExecStart=/opt/xy/xy-server
Restart=on-failure
RestartSec=2

NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
PrivateTmp=true
ReadWritePaths=/var/lib/xy /run/lock
ProtectKernelTunables=true
ProtectControlGroups=true
RestrictAddressFamilies=AF_INET AF_INET6
LockPersonality=true

[Install]
WantedBy=multi-user.target
```

```sh
sudo systemctl daemon-reload
sudo systemctl enable --now xy
journalctl -u xy -n 20
```

## 5. Собственно настраиваем сервинг сайта

Для этого вы сначала должны создать DNS-запись на сайте вашего доменного регистратора, которая будет указывать на IP вашего сервера. 

Скачайте [Caddy](https://caddyserver.com) и пропишите конфиг:

```caddy
xy.ваш-сайт.org {
	encode zstd gzip
	reverse_proxy localhost:9673  # или другой порт, если вы поменяли PORT
}
```

## 6. Создаём первый (админский) аккаунт

```sh
printf 'ваш-пароль' | sudo -u xy XY_DB=/var/lib/xy/xy.db /opt/xy/xy-server adduser ваш_логин
```

Чтобы создать аккаунт для другого человека, идите на <https://xy.ваш-сайт.org/admin/create_users>

## 7. Вход через телеграм

Отдельный процесс не нужен: сервер сам опрашивает Telegram. Получите токен у
[@BotFather](https://t.me/BotFather) и допишите в `/etc/xy.env`:

```sh
XY_BOT_TOKEN=токен-от-BotFather
XY_BOT_NAME=логин_вашего_бота
```

Токен — это заявка на все коды входа, которые придут этому боту. Один токен —
один сервер: если его получат две копии (например, боевая и тестовая), Telegram
отдаст каждой случайную половину сообщений. На одной машине это ловит блокировка
в `/run/lock` (поэтому в юните и стоит `ReadWritePaths=/run/lock`), на разных —
только сам Telegram, и тогда в логе будет `getUpdates: CONFLICT`.

## 8. Бэкапы

```sh
sudo -u xy XY_DB=/var/lib/xy/xy.db XY_BLOBS=/var/lib/xy/blobs \
  /opt/xy/xy-server backup /var/backups/xy/$(date +%F)
```

Чтобы восстановить из бэкапа, остановите systemd-сервисы, положите `xy.db` и `blobs` из бэкапа в
`/var/lib/xy`, сделайте chown `xy:xy`, перезапустите сервисы.

## 9. Обновление

```sh
sudo -u xy … /opt/xy/xy-server backup /var/backups/xy/before-update
sudo systemctl stop xy
# распакуйте новый архив поверх /opt/xy
sudo systemctl start xy
```
